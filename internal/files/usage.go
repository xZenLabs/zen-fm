package files

type Usage struct {
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

func (r *Root) Usage() (Usage, error) { return filesystemUsage(r.name) }
