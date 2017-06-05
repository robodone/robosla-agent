package main

type Executor struct {
	up   *Uplink
	down *Downlink
}

func NewExecutor(up *Uplink, down *Downlink) *Executor {
	return &Executor{up: up, down: down}
}

func (exe *Executor) Run() error {
	// TODO(krasin): implement
	return nil
}
