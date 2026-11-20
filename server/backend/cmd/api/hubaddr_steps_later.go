package main

import (
	"context"
	"errors"
)

// Step 5 arrives with plan task 8; until then it refuses.
var errHubStepNotReady = errors.New("шаг ещё не реализован")

func (e *hubAddrEngine) stepSwitch(context.Context, hubAddrChange, *stepLog) (any, error) {
	return nil, errHubStepNotReady
}
