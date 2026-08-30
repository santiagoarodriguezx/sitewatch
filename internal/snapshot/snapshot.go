package snapshot

import "time"

type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
}

type Link struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

type Price struct {
	Raw      string  `json:"raw"`
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
	Context  string  `json:"context,omitempty"`
}

type Image struct {
	Src string `json:"src"`
	Alt string `json:"alt,omitempty"`
}

type StructuredItem struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

type Fingerprints struct {
	HTML       string `json:"html"`
	Visible    string `json:"visible"`
	Headings   string `json:"headings"`
	Links      string `json:"links"`
	Prices     string `json:"prices"`
	Structured string `json:"structured"`
	Metadata   string `json:"metadata"`
}

type Page struct {
	ID             int64             `json:"id,omitempty"`
	URL            string            `json:"url"`
	CanonicalURL   string            `json:"canonical_url"`
	Title          string            `json:"title"`
	Description    string            `json:"description,omitempty"`
	Headings       []Heading         `json:"headings,omitempty"`
	Paragraphs     []string          `json:"paragraphs,omitempty"`
	Links          []Link            `json:"links,omitempty"`
	Buttons        []string          `json:"buttons,omitempty"`
	Prices         []Price           `json:"prices,omitempty"`
	Images         []Image           `json:"images,omitempty"`
	Meta           map[string]string `json:"meta,omitempty"`
	OpenGraph      map[string]string `json:"open_graph,omitempty"`
	StructuredData []StructuredItem  `json:"structured_data,omitempty"`
	VisibleText    string            `json:"visible_text"`
	Fingerprints   Fingerprints      `json:"fingerprints"`
	FetchedAt      time.Time         `json:"fetched_at"`
	HTTPStatus     int               `json:"http_status"`
}

type Change struct {
	ID       int64   `json:"id,omitempty"`
	Type     string  `json:"type"`
	Entity   string  `json:"entity"`
	Category string  `json:"category"`
	OldValue string  `json:"old_value,omitempty"`
	NewValue string  `json:"new_value,omitempty"`
	Context  string  `json:"context,omitempty"`
	Score    float64 `json:"score"`
}
