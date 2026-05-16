package currentuser

import "sync"

type Dispatcher struct {
	mu         sync.Mutex
	configPath string
	stateDir   string
	helperPIDs map[uint32]int
	started    bool
}

type HelperOptions struct {
	SessionID int
	StateDir  string
	BuildID   string
}

type RoleHealth struct {
	Status     string
	StatusCode string
	Detail     string
	Details    map[string]any
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		helperPIDs: map[uint32]int{},
	}
}
