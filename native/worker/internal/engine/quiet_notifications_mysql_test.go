package engine

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/mocks"
	"os"
	"strings"
	"testing"
	"workbench-native/internal/core"
)

func TestMySQLQuietProgramRetainsEventsAndStillNotifiesFailure(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance_quiet") {
		t.Skip("dedicated quiet-notification acceptance DB required")
	}
	ctx := context.Background()
	s, e := core.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.Migrate(ctx))
	id := uuid.NewString()
	p := core.Principal{Kind: "personal", SessionID: id}
	o := core.Principal{Kind: "owner", SessionID: id + "owner"}
	run := core.Run{ID: id, Principal: p, Operation: "pipeline", Title: "Quiet maintenance", Quiet: true}
	must(s.SaveRun(ctx, run))
	must(s.PutTransport(ctx, o, core.Transport{ID: id, Name: "isolated notification sink", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Enabled: true}))
	settings := core.NotificationSettings{Enabled: true, SessionID: id, Targets: []core.NotificationTarget{{ConnectionID: id, Target: map[string]string{"actorId": id, "conversationId": id}}}}
	raw, _ := json.Marshal(settings)
	_, e = s.DB.ExecContext(ctx, `INSERT INTO wb_settings VALUES('notifications',?) ON DUPLICATE KEY UPDATE body=VALUES(body)`, raw)
	must(e)
	event := core.RunEvent{ID: id + "-published", SessionID: id, RunID: id, Kind: "resource.published", Text: "Published"}
	must(s.Emit(ctx, event))
	a := &Activities{Store: s}
	must(a.QueueNotifications(ctx))
	var count int
	must(s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_deliveries`).Scan(&count))
	if count != 0 {
		t.Fatal("quiet success produced a notification")
	}
	events, e := s.NotificationEvents(ctx, 0)
	must(e)
	if len(events) != 1 {
		t.Fatal("quiet mode removed the Session event")
	}
	saved, e := s.NotificationSettings(ctx)
	must(e)
	if saved.After != events[0].Sequence {
		t.Fatal("quiet event did not advance the cursor")
	}
	temporal := &mocks.Client{}
	temporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&mocks.WorkflowRun{}, nil).Once()
	a.Temporal = temporal
	must(s.Emit(ctx, core.RunEvent{ID: id + "-failed", SessionID: id, RunID: id, Kind: "run.failed", Text: "Refresh failed"}))
	must(a.QueueNotifications(ctx))
	temporal.AssertExpectations(t)
	must(s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_deliveries WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.text')) LIKE '%Refresh failed%'`).Scan(&count))
	if count != 1 {
		t.Fatal("quiet failure notification missing", count)
	}
}
