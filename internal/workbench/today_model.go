package workbench

// All personal features use the existing SQLite database and execution worker.
const personalProjectID = "personal-assistant"
const todaySchema = `
CREATE TABLE IF NOT EXISTS project_tasks(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,operation TEXT UNIQUE NOT NULL,data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS project_contexts(project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS context_versions(project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,version INTEGER NOT NULL,data TEXT NOT NULL,PRIMARY KEY(project_id,version));
CREATE TABLE IF NOT EXISTS context_jobs(project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,due_at INTEGER NOT NULL,data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS schedule_checks(schedule_id TEXT NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,operation TEXT NOT NULL,checked_at INTEGER NOT NULL,outcome TEXT NOT NULL,run_id TEXT NOT NULL DEFAULT '',detail TEXT NOT NULL DEFAULT '',PRIMARY KEY(schedule_id,operation));
CREATE TABLE IF NOT EXISTS schedule_inputs(schedule_id TEXT PRIMARY KEY REFERENCES schedules(id) ON DELETE CASCADE, fingerprint TEXT NOT NULL, sources TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS personal_understanding_versions(version INTEGER PRIMARY KEY,data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS personal_memory_progress(id TEXT PRIMARY KEY,version INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS personal_memory_exclusions(id TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS personal_memory_revisions(id TEXT NOT NULL,version INTEGER NOT NULL,data TEXT NOT NULL,PRIMARY KEY(id,version));
CREATE TABLE IF NOT EXISTS personal_memories(id TEXT PRIMARY KEY,data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS personal_decisions(id TEXT PRIMARY KEY,operation TEXT UNIQUE NOT NULL,data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS daily_contents(day TEXT PRIMARY KEY,data TEXT NOT NULL);
`

type ProjectTask struct {
	ID            string `json:"id"`
	ProjectID     string `json:"projectId"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	State         string `json:"state"`
	ScheduledDate string `json:"scheduledDate,omitempty"`
	DueDate       string `json:"dueDate,omitempty"`
	Source        string `json:"source,omitempty"`
	Version       int64  `json:"version"`
	UpdatedAt     int64  `json:"updatedAt"`
	SessionID     string `json:"sessionId,omitempty"`
	DraftVersion  int64  `json:"draftVersion,omitempty"`
	DraftText     string `json:"draftText,omitempty"`
	Operation     string `json:"operation,omitempty"`
}
type ContextSource struct {
	ID     string `json:"id"`
	Hash   string `json:"hash"`
	At     int64  `json:"at"`
	Status string `json:"status"`
	Text   string `json:"text,omitempty"`
}
type ProjectContext struct {
	PublishedBy      string          `json:"publishedBy,omitempty"`
	PublishedVersion int64           `json:"publishedVersion,omitempty"`
	ManualAt         int64           `json:"manualAt,omitempty"`
	Coverage         map[string]int  `json:"coverage"`
	ProjectID        string          `json:"projectId"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Version          int64           `json:"version"`
	UpdatedAt        int64           `json:"updatedAt"`
	State            string          `json:"state"`
	Error            string          `json:"error,omitempty"`
	Recent           []ContextChange `json:"recent"`
	Sources          []ContextSource `json:"sources,omitempty"`
	Fingerprint      string          `json:"fingerprint"`
	Manual           string          `json:"manual,omitempty"`
}
type ContextChange struct {
	Text   string `json:"text"`
	At     int64  `json:"at"`
	Source string `json:"source"`
}
type PersonalMemory struct {
	Correction bool             `json:"correction,omitempty"`
	BaseRefs   map[string]int64 `json:"baseRefs,omitempty"`
	Quote      string           `json:"quote,omitempty"`
	Confirmed  bool             `json:"confirmed"`
	Protected  bool             `json:"protected"`
	Replaces   []string         `json:"replaces,omitempty"`
	ID         string           `json:"id"`
	Text       string           `json:"text"`
	Kind       string           `json:"kind"`
	ExpiresAt  int64            `json:"expiresAt,omitempty"`
	Source     string           `json:"source,omitempty"`
	Version    int64            `json:"version"`
	Forgotten  bool             `json:"forgotten,omitempty"`
	UpdatedAt  int64            `json:"updatedAt"`
}
type DecisionOption struct {
	ID          string       `json:"id"`
	Label       string       `json:"label"`
	Tradeoff    string       `json:"tradeoff"`
	Recommended bool         `json:"recommended"`
	Task        *ProjectTask `json:"task,omitempty"`
}
type PersonalDecision struct {
	Dispatched     bool             `json:"dispatched,omitempty"`
	ID             string           `json:"id"`
	Question       string           `json:"question"`
	Reason         string           `json:"reason"`
	Options        []DecisionOption `json:"options"`
	Version        int64            `json:"version"`
	State          string           `json:"state"`
	Answer         string           `json:"answer,omitempty"`
	Other          string           `json:"other,omitempty"`
	TaskID         string           `json:"taskId,omitempty"`
	SessionID      string           `json:"sessionId"`
	SourceRun      string           `json:"sourceRun,omitempty"`
	ContextVersion string           `json:"contextVersion"`
	CreatedAt      int64            `json:"createdAt"`
}
type DailyContent struct {
	PersonalRulesVersion int64  `json:"personalRulesVersion,omitempty"`
	DecisionID           string `json:"decisionId,omitempty"`
	Day                  string `json:"day"`
	Version              int64  `json:"version"`
	Text                 string `json:"text"`
	State                string `json:"state"`
	Error                string `json:"error,omitempty"`
	UpdatedAt            int64  `json:"updatedAt"`
	ContextVersion       string `json:"contextVersion"`
}
