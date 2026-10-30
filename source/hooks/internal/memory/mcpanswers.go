package memory

// What each MCP tool answers beside its message. Keys are only ever added, and
// a list is empty rather than absent: the absent-key rule of the reads covers
// read answers alone.

func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}

	return values
}

type idFields struct {
	ID string `json:"id"`
}

type idsFields struct {
	IDs []string `json:"ids"`
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
