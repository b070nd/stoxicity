package scheduler

// queue is a min-heap of sources ordered by next-due time, then by ID so that
// sources due at the same moment start in a fixed order. It implements
// container/heap.Interface and keeps each entry's index current.
type queue []*entry

func (q queue) Len() int { return len(q) }

func (q queue) Less(i, j int) bool {
	a, b := q[i].health.NextDue, q[j].health.NextDue
	if !a.Equal(b) {
		return a.Before(b)
	}
	return q[i].src.ID < q[j].src.ID
}

func (q queue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index = i
	q[j].index = j
}

func (q *queue) Push(x any) {
	e := x.(*entry)
	e.index = len(*q)
	*q = append(*q, e)
}

func (q *queue) Pop() any {
	old := *q
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.index = -1
	*q = old[:n-1]
	return e
}
