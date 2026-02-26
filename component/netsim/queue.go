package netsim

// Queue discipline types.
// Currently the simulation applies inline delays and drops.
// Queue types affect how packets are buffered under congestion:
// - fifo: default, first-in first-out (no special handling needed)
// - prio: priority queue (reserved for future per-flow priority)
// - fair-queue: round-robin per flow (reserved for future)
//
// The queue-type field is parsed and stored in config.
// FIFO is the active implementation; prio and fair-queue are recognized
// but behave as FIFO until advanced queue management is needed.

const (
	QueueFIFO      = "fifo"
	QueuePriority  = "prio"
	QueueFairQueue = "fair-queue"
)

func ValidQueueType(qt string) bool {
	switch qt {
	case QueueFIFO, QueuePriority, QueueFairQueue, "":
		return true
	}
	return false
}
