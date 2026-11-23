package domain

import "slices"

const (
	EdgeIntegration = "integration"
	EdgeDeployment  = "deployment"

	PlanActive    = "active"
	PlanCancelled = "cancelled"
)

type PlanEdge struct {
	From string
	To   string
	Kind string
}

func ValidatePlanEdges(tasks []string, edges []PlanEdge) error {
	if len(tasks) < 2 {
		return invalidInput("a plan needs at least two tasks")
	}
	seen := make(map[string]struct{}, len(tasks))
	for _, key := range tasks {
		if key == "" {
			return invalidInput("plan tasks must not be blank")
		}
		if _, ok := seen[key]; ok {
			return invalidInput("a task appears twice in the plan")
		}
		seen[key] = struct{}{}
	}
	incoming := make(map[string][]string, len(tasks))
	unique := make(map[[2]string]struct{}, len(edges))
	for _, edge := range edges {
		if edge.Kind != EdgeIntegration && edge.Kind != EdgeDeployment {
			return invalidInput("plan edge kind must be integration or deployment")
		}
		if _, ok := seen[edge.From]; !ok {
			return invalidInput("plan edge leaves the plan")
		}
		if _, ok := seen[edge.To]; !ok {
			return invalidInput("plan edge enters the plan")
		}
		if edge.From == edge.To {
			return invalidInput("a plan edge cannot join a task with itself")
		}
		key := [2]string{edge.From, edge.To}
		if _, ok := unique[key]; ok {
			return invalidInput("a plan edge appears twice")
		}
		unique[key] = struct{}{}
		incoming[edge.To] = append(incoming[edge.To], edge.From)
	}
	remaining := make(map[string]int, len(tasks))
	for _, key := range tasks {
		remaining[key] = len(incoming[key])
	}
	queue := make([]string, 0, len(tasks))
	for _, key := range tasks {
		if remaining[key] == 0 {
			queue = append(queue, key)
		}
	}
	visited := 0
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		visited++
		for _, other := range tasks {
			for _, from := range incoming[other] {
				if from == key {
					remaining[other]--
					if remaining[other] == 0 {
						queue = append(queue, other)
					}
				}
			}
		}
	}
	if visited != len(tasks) {
		return invalidInput("the plan has a cycle")
	}
	return nil
}

func Unsatisfied(edges []PlanEdge, released, deployed map[string]struct{}, task string) []string {
	waiting := make([]string, 0)
	for _, edge := range edges {
		if edge.To != task {
			continue
		}
		if _, ok := released[edge.From]; !ok {
			waiting = append(waiting, edge.From)
			continue
		}
		if edge.Kind != EdgeDeployment {
			continue
		}
		if _, ok := deployed[edge.From]; !ok {
			waiting = append(waiting, edge.From)
		}
	}
	slices.Sort(waiting)
	return waiting
}
