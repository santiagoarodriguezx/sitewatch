package notifier

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/sitewatch/sitewatch/internal/fetcher"
	"github.com/sitewatch/sitewatch/internal/output"
	"github.com/sitewatch/sitewatch/internal/snapshot"
)

type Notification struct {
	Monitor   string            `json:"monitor"`
	URL       string            `json:"url"`
	Changes   []snapshot.Change `json:"changes"`
	Timestamp time.Time         `json:"timestamp"`
}
type Notifier interface {
	Notify(context.Context, Notification) error
}
type Console struct{ Writer io.Writer }

func (n Console) Notify(_ context.Context, x Notification) error {
	output.Changes(n.Writer, x.Changes)
	return nil
}

type Webhook struct {
	URL    string
	Client *fetcher.Client
}

func (n Webhook) Notify(ctx context.Context, x Notification) error {
	b, err := json.Marshal(x)
	if err != nil {
		return err
	}
	return n.Client.PostJSON(ctx, n.URL, b)
}
