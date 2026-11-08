package guard

import (
	"testing"

	"oai_bot/internal/model"
)

func TestDirectJoinNeedsVerify(t *testing.T) {
	if !needsJoinVerify(model.VerifyJoinRequest) || !needsJoinVerify(model.VerifyRestrict) || needsJoinVerify(model.VerifyOff) {
		t.Fatal("join 模式直接进群也必须验证")
	}
}
