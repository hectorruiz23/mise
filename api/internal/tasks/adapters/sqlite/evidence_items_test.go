package sqlite_test

import (
	"context"
	"encoding/json"
	"testing"

	"mise/api/internal/tasks/adapters/sqlite"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

func label(value string) *string {
	return &value
}

func TestDeliverEvidenceItemsStoresObjects(t *testing.T) {
	database, _ := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	created, _ := createTask(t, service, app.CreateParams{Title: "Structured"})
	if _, err := service.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 1, true); err != nil {
		t.Fatalf("move to ready: %v", err)
	}
	_, lease, err := service.Acquire(context.Background(), created.Key, agent())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	items := []domain.EvidenceItem{
		{Label: label("tests"), Raw: "go test ./..."},
		{Label: label("build"), Raw: "make accept"},
	}
	_, delivery, err := service.DeliverItems(
		context.Background(), created.Key, agent(), &lease.ID, "Structured evidence", items, nil)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(delivery.Evidence) != 2 || len(delivery.EvidenceItems) != 2 {
		t.Fatalf("delivery = %+v", delivery)
	}
	if delivery.EvidenceItems[0].Label == nil || *delivery.EvidenceItems[0].Label != "tests" {
		t.Fatalf("delivery items = %+v", delivery.EvidenceItems)
	}

	var stored string
	if err := database.QueryRow(
		`SELECT evidence FROM deliveries WHERE id = ?`, delivery.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored evidence: %v", err)
	}
	var storedItems []struct {
		Label *string `json:"label"`
		Raw   string  `json:"raw"`
	}
	if err := json.Unmarshal([]byte(stored), &storedItems); err != nil {
		t.Fatalf("stored evidence is not an object array: %v (%s)", err, stored)
	}
	if len(storedItems) != 2 || storedItems[0].Label == nil || *storedItems[0].Label != "tests" {
		t.Fatalf("stored items = %+v", storedItems)
	}

	repo := sqlite.New(database)
	deliveries, err := repo.ListDeliveries(context.Background(), created.Key, app.AllProjects)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(deliveries) != 1 || len(deliveries[0].EvidenceItems) != 2 {
		t.Fatalf("deliveries = %+v", deliveries)
	}
	if deliveries[0].Evidence[0] != deliveries[0].EvidenceItems[0].Raw {
		t.Fatalf("representations diverged: %+v", deliveries[0])
	}
}

func TestLegacyEvidenceReadsAsUnlabelledItems(t *testing.T) {
	database, _ := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	created, _ := createTask(t, service, app.CreateParams{Title: "Legacy"})
	if _, err := database.Exec(`
		INSERT INTO deliveries (task_key, lease_id, summary, evidence, created_at, actor_id)
		VALUES (?, NULL, 'Legacy', ?, ?, 1)`,
		created.Key, `["old proof","/home/hector/file"]`, testNow); err != nil {
		t.Fatalf("insert legacy delivery: %v", err)
	}

	repo := sqlite.New(database)
	deliveries, err := repo.ListDeliveries(context.Background(), created.Key, app.AllProjects)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %+v", deliveries)
	}
	if len(deliveries[0].EvidenceItems) != 2 {
		t.Fatalf("items = %+v", deliveries[0].EvidenceItems)
	}
	for index, item := range deliveries[0].EvidenceItems {
		if item.Label != nil || item.Raw != deliveries[0].Evidence[index] {
			t.Fatalf("legacy item %d = %+v", index, item)
		}
	}
}

func TestUnreadableEvidenceFailsListing(t *testing.T) {
	database, _ := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	created, _ := createTask(t, service, app.CreateParams{Title: "Broken"})
	if _, err := database.Exec(`
		INSERT INTO deliveries (task_key, lease_id, summary, evidence, created_at, actor_id)
		VALUES (?, NULL, 'Broken', ?, ?, 1)`,
		created.Key, `["ok",{"label":5,"raw":"broken"}]`, testNow); err != nil {
		t.Fatalf("insert broken delivery: %v", err)
	}

	repo := sqlite.New(database)
	if _, err := repo.ListDeliveries(context.Background(), created.Key, app.AllProjects); err == nil {
		t.Fatal("unreadable evidence was silently accepted")
	}
}
