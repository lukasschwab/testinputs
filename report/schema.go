// Package report defines the shared, versioned static and runtime evidence schema.
package report

const SchemaVersion = 2

type Location struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// Finding separates potential static dependencies from observed runtime access.
// Neither evidence kind asserts a reproduced cache miss. Runtime findings omit
// source locations because the internal test log does not contain them.
type Finding struct {
	Package    string `json:"package"`
	Rule       string `json:"rule"`
	Operation  string `json:"operation"`
	Evidence   string `json:"evidence"`
	Confidence string `json:"confidence"`
	Reason     string `json:"reason"`
	Expression string `json:"expression,omitempty"`
	Path       string `json:"path,omitempty"`
	// Environment names are distinct from filesystem paths. Values are never
	// collected, hashed, or reported.
	Environment   string   `json:"environment,omitempty"`
	Location      Location `json:"location,omitzero"`
	Source        Location `json:"source,omitzero"`
	Class         string   `json:"class,omitempty"`
	CacheRelevant bool     `json:"cache_relevant,omitzero"`
	Count         int      `json:"count,omitempty"`
	Suppressed    bool     `json:"suppressed"`
}
