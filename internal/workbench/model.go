package workbench

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("找不到这条记录")
var ErrLocked = errors.New("项目正在执行任务，请先停止任务或等待完成后再保存")
var ErrConflict = errors.New("文件已有新版本，请重新读取后再保存")

func newID() string       { return uuid.NewString() }
func now() int64          { return time.Now().UnixMilli() }
func encode(v any) string { b, _ := json.Marshal(v); return string(b) }

type Project struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Aliases          []string `json:"aliases"`
	Description      string   `json:"description"`
	DefaultSessionID string   `json:"defaultSessionId"`
	ExpertIDs        []string `json:"expertIds"`
	DefaultExpertID  string   `json:"defaultExpertId"`
	PluginIDs        []string `json:"pluginIds"`
	Archived         bool     `json:"archived"`
	CreatedAt        int64    `json:"createdAt"`
}
type Session struct {
	PinnedAt       int64  `json:"pinnedAt,omitempty"`
	RecycledAt     int64  `json:"recycledAt,omitempty"`
	ExpiresAt      int64  `json:"expiresAt,omitempty"`
	RecycleReason  string `json:"recycleReason,omitempty"`
	Lifecycle      string `json:"lifecycle,omitempty"`
	ArchivePath    string `json:"archivePath,omitempty"`
	LifecycleError string `json:"lifecycleError,omitempty"`
	Draft          bool   `json:"draft,omitempty"`
	TitleSource    string `json:"titleSource,omitempty"`
	ID             string `json:"id"`
	ProjectID      string `json:"projectId"`
	HarnessID      string `json:"harnessId"`
	Title          string `json:"title"`
	Kind           string `json:"kind"`
	ExpertID       string `json:"expertId"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}
type Expert struct {
	UpdatedAt   int64    `json:"updatedAt"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	SkillName   string   `json:"skillName"`
	Description string   `json:"description"`
	Digest      string   `json:"digest"`
	Tools       []string `json:"tools"`
	CreatedAt   int64    `json:"createdAt"`
}
type Incoming struct {
	Source         string          `json:"source"`
	ConnectionID   string          `json:"connectionId"`
	ActorID        string          `json:"actorId"`
	ConversationID string          `json:"conversationId"`
	EventID        string          `json:"eventId"`
	ReplyTo        string          `json:"replyTo,omitempty"`
	SessionID      string          `json:"sessionId,omitempty"`
	Text           string          `json:"text"`
	Attachments    []string        `json:"attachments,omitempty"`
	ExpertID       string          `json:"expertId,omitempty"`
	References     []FileReference `json:"references,omitempty"`
}
type FileReference struct {
	ProjectID string `json:"projectId"`
	Path      string `json:"path"`
}
type Message struct {
	ID           string          `json:"id"`
	Incoming     Incoming        `json:"incoming"`
	State        string          `json:"state"`
	SessionID    string          `json:"sessionId,omitempty"`
	RunID        string          `json:"runId,omitempty"`
	Question     string          `json:"question,omitempty"`
	Candidates   []Candidate     `json:"candidates,omitempty"`
	References   []FileReference `json:"references,omitempty"`
	CreatedAt    int64           `json:"createdAt"`
	IncomingHash string          `json:"incomingHash,omitempty"`
}
type Candidate struct {
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId"`
	Label     string `json:"label"`
}
type Run struct {
	Kind           string            `json:"kind,omitempty"`
	ExpertName     string            `json:"expertName,omitempty"`
	ID             string            `json:"id"`
	ProjectID      string            `json:"projectId"`
	SessionID      string            `json:"sessionId"`
	MessageID      string            `json:"messageId"`
	EventID        string            `json:"eventId,omitempty"`
	State          string            `json:"state"`
	Source         string            `json:"source"`
	Text           string            `json:"text"`
	ExpertID       string            `json:"expertId"`
	ExpertDigest   string            `json:"expertDigest"`
	ExpertVersions map[string]string `json:"expertVersions,omitempty"`
	Attachments    []string          `json:"attachments,omitempty"`
	ReplyAnchor    string            `json:"replyAnchor,omitempty"`
	AnchorExplicit bool              `json:"anchorExplicit"`
	Result         string            `json:"result"`
	Error          string            `json:"error,omitempty"`
	ScheduleID     string            `json:"scheduleId,omitempty"`
	CreatedAt      int64             `json:"createdAt"`
	StartedAt      int64             `json:"startedAt"`
	FinishedAt     int64             `json:"finishedAt"`
}
type Event struct {
	Seq       int64           `json:"seq"`
	SessionID string          `json:"sessionId,omitempty"`
	RunID     string          `json:"runId,omitempty"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt int64           `json:"createdAt"`
}
type Interaction struct {
	ID        string          `json:"id"`
	RunID     string          `json:"runId"`
	SessionID string          `json:"sessionId"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	State     string          `json:"state"`
	CreatedAt int64           `json:"createdAt"`
}
type Connection struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Label         string `json:"label"`
	OwnerID       string `json:"ownerId"`
	Enabled       bool   `json:"enabled"`
	Status        string `json:"status"`
	LastHeartbeat int64  `json:"lastHeartbeat"`
}
type Delivery struct {
	ID             string   `json:"id"`
	MessageID      string   `json:"messageId"`
	RunID          string   `json:"runId"`
	SessionID      string   `json:"sessionId"`
	ConnectionID   string   `json:"connectionId"`
	ConversationID string   `json:"conversationId"`
	ActorID        string   `json:"actorId"`
	ReplyTo        string   `json:"replyTo"`
	Kind           string   `json:"kind"`
	Text           string   `json:"text"`
	State          string   `json:"state"`
	Receipt        string   `json:"receipt"`
	Receipts       []string `json:"receipts,omitempty"`
	Error          string   `json:"error"`
	CreatedAt      int64    `json:"createdAt"`
}
type Schedule struct {
	PausedReason         string `json:"pausedReason,omitempty"`
	ID                   string `json:"id"`
	ProjectID            string `json:"projectId"`
	Name                 string `json:"name"`
	ExpertID             string `json:"expertId"`
	Instruction          string `json:"instruction"`
	Cron                 string `json:"cron"`
	Timezone             string `json:"timezone"`
	Enabled              bool   `json:"enabled"`
	NextAt               int64  `json:"nextAt"`
	NotifyConnectionID   string `json:"notifyConnectionId"`
	NotifyConversationID string `json:"notifyConversationId"`
	CreatedAt            int64  `json:"createdAt"`
}
type FileInfo struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Hash       string `json:"hash"`
	ModifiedAt int64  `json:"modifiedAt"`
	Directory  bool   `json:"directory"`
}
type Revision struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	RunID     string `json:"runId"`
	Path      string `json:"path"`
	Before    string `json:"before"`
	After     string `json:"after"`
	CreatedAt int64  `json:"createdAt"`
}

type Config struct {
	BackupScrubCommand []string
	DataDir            string
	BasePath           string
	PublicURL          string
	WebDir             string
	ChannelToken       string
	PasswordHash       string
	HarnessCommand     []string
	HarnessPatch       string
	HarnessHome        string
	Provider           string
	Model              string
	Podman             string
	RuntimeImage       string
	RuntimeMode        string
	BrowserURL         string
	RuntimeDir         string
}
