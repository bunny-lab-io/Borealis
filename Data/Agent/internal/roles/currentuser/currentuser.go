package currentuser

type Dispatcher struct{}

type RoleHealth struct {
	Status     string
	StatusCode string
	Detail     string
	Details    map[string]any
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{}
}
