package buildinfo

import (
	"encoding/json"
	"net/http"
)

var (
	revision   string
	treeState  = "unknown"
	repository string
)

type Info struct {
	Repository *string `json:"repository"`
	Revision   *string `json:"revision"`
	TreeState  string  `json:"tree_state"`
}

func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(Info{
			Repository: optional(repository),
			Revision:   optional(revision),
			TreeState:  state(),
		})
	}
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func state() string {
	if treeState == "" {
		return "unknown"
	}
	return treeState
}
