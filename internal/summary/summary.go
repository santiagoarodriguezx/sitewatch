package summary

import (
	"context"
	"fmt"

	"github.com/sitewatch/sitewatch/internal/snapshot"
)

type ChangeSummarizer interface {
	Summarize(context.Context, []snapshot.Change) (string, error)
}
type Deterministic struct{}

func (Deterministic) Summarize(_ context.Context, changes []snapshot.Change) (string, error) {
	high := 0
	for _, c := range changes {
		if c.Score >= .8 {
			high++
		}
	}
	noun := "changes"
	if len(changes) == 1 {
		noun = "change"
	}
	return fmt.Sprintf("%d meaningful %s (%d important, %d medium)", len(changes), noun, high, len(changes)-high), nil
}
