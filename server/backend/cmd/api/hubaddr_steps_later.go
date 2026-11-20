package main

import (
	"context"
	"errors"
)

// Steps 3–5 arrive with plan tasks 7–8; until then they refuse.
var errHubStepNotReady = errors.New("шаг ещё не реализован")

func (e *hubAddrEngine) stepNodes(context.Context, hubAddrChange, hubAddrStep, *stepLog) (any, error) {
	return nil, errHubStepNotReady
}
func (e *hubAddrEngine) stepDNS(context.Context, hubAddrChange, hubAddrStep, *stepLog) (any, error) {
	return nil, errHubStepNotReady
}
func (e *hubAddrEngine) stepSwitch(context.Context, hubAddrChange, *stepLog) (any, error) {
	return nil, errHubStepNotReady
}
