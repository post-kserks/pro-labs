package protocol

type Request struct {
	ID    string `json:"id"`
	Token string `json:"token,omitempty"`
	Query string `json:"query"`
}

type Response struct {
	ID       string     `json:"id"`
	Status   string     `json:"status"`
	Type     string     `json:"type"`
	Columns  []string   `json:"columns"`
	Rows     [][]string `json:"rows"`
	Affected int        `json:"affected"`
	Message  string     `json:"message,omitempty"`
	AsOfNote string     `json:"as_of_note,omitempty"`
}
