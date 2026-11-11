package engine

import (
	"context"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/types/known/durationpb"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"workbench-native/internal/core"
)

func TestRealReplyCommittedBeforeWorkflowStartupRecovers(t *testing.T) {
	testRealReplyRecovery(t, false, 0, 0, []string{"immutable recovered reply"})
}

func TestRealReplyPartsSendSeparatelyAndInOrder(t *testing.T) {
	testRealReplyRecovery(t, false, 50, 40, []string{strings.Repeat("甲", 40), strings.Repeat("乙", 11)})
}

func TestRealReplyShortParagraphsUseSeparateBubblesWithoutToolLimits(t *testing.T) {
	testRealReplyRecovery(t, true, 0, 0, []string{"第一段。", "第二段。"})
}

func TestRealReplyFiftyCharactersStayInOneMessage(t *testing.T) {
	testRealReplyRecovery(t, false, 50, 40, []string{strings.Repeat("甲", 50)})
}

func TestRealReplyPartsReconcileWhenCallbackArrivesEarly(t *testing.T) {
	testRealReplyRecovery(t, true, 50, 40, []string{strings.Repeat("甲", 40), strings.Repeat("乙", 11)})
}

func testRealReplyRecovery(t *testing.T, earlyCallback bool, singleLimit, partLimit int, expected []string) {
	dsn, address := os.Getenv("WORKBENCH_TEST_REPLY_RECOVERY_DSN"), os.Getenv("WORKBENCH_TEST_TEMPORAL")
	if dsn == "" || address == "" {
		t.Skip("dedicated reply recovery database and Temporal not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated DB required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	s, e := core.Open(ctx, dsn)
	must(e)
	defer func() { s.DB.Close() }()
	must(s.Migrate(ctx))
	id := "recovery-" + uuid.NewString()
	owner := core.Principal{Kind: "owner", SessionID: id + "owner"}
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "reply recovery"}))
	var sentMu sync.Mutex
	var sentTexts, sentKeys []string
	server := mcp.NewServer(&mcp.Implementation{Name: "recovery-fixture", Version: "1"}, nil)
	type Args struct {
		ConnectionID   string            `json:"connectionId"`
		IdempotencyKey string            `json:"idempotencyKey"`
		Target         map[string]string `json:"target"`
		Text           string            `json:"text"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "message_send"}, func(_ context.Context, _ *mcp.CallToolRequest, a Args) (*mcp.CallToolResult, Receipt, error) {
		sentMu.Lock()
		sentTexts = append(sentTexts, a.Text)
		sentKeys = append(sentKeys, a.IdempotencyKey)
		sentMu.Unlock()
		state := "sent"
		if len(expected) > 1 && len(sentKeys) == 1 {
			state = "accepted"
		}
		return nil, Receipt{State: state, Receipt: "fixture-" + a.IdempotencyKey}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "message_receipt"}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		ConnectionID   string            `json:"connectionId"`
		IdempotencyKey string            `json:"idempotencyKey"`
		Target         map[string]string `json:"target"`
		Receipt        string            `json:"receipt"`
		State          string            `json:"state"`
	}) (*mcp.CallToolResult, Receipt, error) {
		state := in.State
		if earlyCallback {
			state = "sent"
		}
		return nil, Receipt{State: state, Receipt: in.Receipt}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer endpoint.Close()
	t.Setenv("WORKBENCH_CONNECTOR_TEST", strings.Repeat("x", 40))
	must(s.PutTransport(ctx, owner, core.Transport{ID: id, Kind: "wecom-gateway", Name: "fixture", Endpoint: endpoint.URL, TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}))
	_, e = s.PutReplySettings(ctx, owner, core.ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "customer-reply", Enabled: true})
	must(e)
	in := core.IncomingMessage{ConnectionID: id, ConversationID: "one", ActorID: "one", EventID: "one", Text: "synthetic question"}
	route, e := s.EnsureReplyRoute(ctx, in)
	must(e)
	_, e = s.AcceptMessage(ctx, in)
	must(e)
	body := strings.Join(expected, "\n")
	original, e := s.PrepareReplyWithLimits(ctx, route.Principal, id, in.EventID, body, singleLimit, partLimit)
	must(e)
	// Simulate loss of the API process after SQL commit and before Temporal start.
	must(s.DB.Close())
	s, e = core.Open(ctx, dsn)
	must(e)
	ns := "reply-recovery-" + uuid.NewString()
	c, e := client.Dial(client.Options{HostPort: address, Namespace: ns})
	must(e)
	defer c.Close()
	_, e = c.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: ns, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour)})
	must(e)
	a := &Activities{Store: s, Temporal: c} // No Model: recovery cannot regenerate.
	must(a.QueueReceiptChecks(ctx))
	w := worker.New(c, InteractiveQueue, worker.Options{})
	w.RegisterWorkflow(SendMessage)
	w.RegisterWorkflow(SubmitMessage)
	w.RegisterActivity(a)
	must(w.Start())
	defer w.Stop()
	var expectedKeys []string
	for i, text := range expected {
		partID := core.ReplyPartID(original.ID, i+1)
		expectedKeys = append(expectedKeys, partID)
		if i == 0 && len(expected) > 1 && !earlyCallback {
			for {
				d, err := s.Delivery(ctx, partID)
				must(err)
				if d.State == "accepted" {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("first part did not reach accepted")
				case <-time.After(50 * time.Millisecond):
				}
			}
		} else {
			must(c.GetWorkflow(ctx, partID, "").Get(ctx, nil))
		}
		d, err := s.Delivery(ctx, partID)
		must(err)
		if i == 0 && len(expected) > 1 && !earlyCallback {
			if d.State != "accepted" {
				t.Fatal("expected queue acceptance, got", d.State)
			}
			if _, err = s.Delivery(ctx, core.ReplyPartID(original.ID, 2)); !core.IsMissing(err) {
				t.Fatal("receipt has not confirmed send, but the second part already exists", err)
			}
			// Simulate the authenticated gateway callback and duplicate callback.
			for _, state := range []string{"sent", "delivered"} {
				d, err = s.FinishDelivery(ctx, partID, state, d.Receipt, "")
				must(err)
				must(StartNextReplyPart(ctx, s, c, d))
			}
		}
		if (d.State != "sent" && d.State != "delivered") || d.Text != text || d.RunID != original.ID {
			t.Fatal("recovery changed the stored delivery", d)
		}
	}
	must(a.QueueReceiptChecks(ctx))
	retry, e := s.PrepareReplyWithLimits(ctx, route.Principal, id, in.EventID, body, singleLimit, partLimit)
	must(e)
	sentMu.Lock()
	defer sentMu.Unlock()
	if retry.ID != original.ID || !slices.Equal(sentTexts, expected) || !slices.Equal(sentKeys, expectedKeys) {
		t.Fatal("delivery was changed, duplicated or out of order", sentTexts, sentKeys)
	}
}
