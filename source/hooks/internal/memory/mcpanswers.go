package memory

// What each MCP tool answers beside its message. Keys are only ever added, as
// for the reads.

type idFields struct {
	ID string `json:"id"`
}

type idsFields struct {
	IDs []string `json:"ids"`
}

type filedFields struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Kind        string        `json:"kind"`
	Topics      []string      `json:"topics"`
	Superseded  []string      `json:"superseded"`
	Attachments []string      `json:"attachments"`
	NotLinked   []string      `json:"not_linked"`
	NearTopics  []jsonNear    `json:"near_topics"`
	Similar     []jsonSimilar `json:"similar"`
}

// jsonNear is a topic new to the store with the existing topics close to it,
// closest first.
type jsonNear struct {
	Topic string   `json:"topic"`
	Near  []string `json:"near"`
}

type jsonSimilar struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Score float64 `json:"score"`
}

type retainedFields struct {
	IDs       []string `json:"ids"`
	Retention string   `json:"retention"`
}

type supersededFields struct {
	IDs     []string `json:"ids"`
	By      string   `json:"by,omitempty"`
	Because string   `json:"because,omitempty"`
}

type retitledFields struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type topicsFields struct {
	ID     string   `json:"id"`
	Topics []string `json:"topics"`
}

type kindFields struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

type ackFields struct {
	IDs      []string `json:"ids"`
	Declined string   `json:"declined,omitempty"`
}

type attachedFields struct {
	ID          string   `json:"id"`
	Attachments []string `json:"attachments"`
}

type readFields struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	NextOffset *int   `json:"next_offset,omitempty"`
	Path       string `json:"path,omitempty"`
}

type linkFields struct {
	ID     string `json:"id"`
	Target string `json:"target"`
}

type seatFields struct {
	Seat  string `json:"seat"`
	Shelf string `json:"shelf"`
}

type renamedFields struct {
	Seat string `json:"seat"`
	Was  string `json:"was"`
}

type releasedFields struct {
	Seat     string `json:"seat"`
	Released bool   `json:"released"`
}

type followFields struct {
	Shelf   string   `json:"shelf"`
	Follows []string `json:"follows"`
}

type mergedFields struct {
	From string `json:"from"`
	Into string `json:"into"`
}

type lifetimeFields struct {
	Shelf     string `json:"shelf"`
	Temporary bool   `json:"temporary"`
}

type droppedFields struct {
	Shelf   string `json:"shelf"`
	Seats   int    `json:"seats"`
	Entries int    `json:"entries"`
}

type trustedFields struct {
	Checkout string `json:"checkout"`
}
