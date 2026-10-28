package ingest

// State is what the ingester is doing with the access log.
type State string

// The states of an ingester.
const (
	// StateWaiting means nothing has been read yet: no poll has run, or the file does not exist.
	StateWaiting State = "waiting"
	// StateFollowing means the last poll worked.
	StateFollowing State = "following"
	// StateFailing means the last poll failed; Status.Error says why.
	StateFailing State = "failing"
)

// Status is what an ingester reports about itself.
type Status struct {
	State State
	// Path is the access log file.
	Path string
	// Offset, SkippedLines and Gaps are those of the stored cursor.
	Offset       int64
	SkippedLines int64
	Gaps         int64
	// Error is why the last poll failed; empty otherwise.
	Error string
}
