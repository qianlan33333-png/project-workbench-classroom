package api

import (
	"testing"
	"workbench-native/internal/core"
)

func TestOperationsDeliveryLabelUsesChannelEvidence(t *testing.T) {
	head := core.OperationsReview{Confirm: "approved"}
	for state, want := range map[string]string{
		"accepted": "CRM 已受理", "pending_confirmation": "待手机确认",
		"sent": "已发送", "delivered": "已送达", "unknown": "回执待核实",
	} {
		got, _ := operationsDeliveryLabel(head, core.OperationsBatchReceipt{State: state}, core.OperationsReviewRow{})
		if got != want {
			t.Errorf("%s shows %q, want %q", state, got, want)
		}
	}
}
