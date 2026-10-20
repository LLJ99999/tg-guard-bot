package guard

import (
	"context"
	"errors"
	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"oai_bot/internal/config"
	"testing"
)

func TestStagedDeploymentIgnoresGroupEvents(t *testing.T) {
	g := &Guard{cfg: &config.Config{Bot: config.Bot{ObserveOnly: true, AllowedGroups: []int64{-1001}}}}
	events := []*models.Update{
		{Message: &models.Message{Chat: models.Chat{ID: -1001, Type: models.ChatTypeSupergroup}}},
		{EditedMessage: &models.Message{Chat: models.Chat{ID: -1001}}},
		{ChatMember: &models.ChatMemberUpdated{Chat: models.Chat{ID: -1001}}},
		{ChatJoinRequest: &models.ChatJoinRequest{Chat: models.Chat{ID: -1001}}},
		{MyChatMember: &models.ChatMemberUpdated{Chat: models.Chat{ID: -1001}}},
		{CallbackQuery: &models.CallbackQuery{}},
	}
	for _, u := range events {
		g.dispatch(context.Background(), nil, u)
	}
	g.cfg.Bot.ObserveOnly = false
	for _, id := range []int64{-1001, -1002} {
		for _, u := range []*models.Update{
			{EditedMessage: &models.Message{Chat: models.Chat{ID: id}}},
			{ChatMember: &models.ChatMemberUpdated{Chat: models.Chat{ID: id}}},
			{ChatJoinRequest: &models.ChatJoinRequest{Chat: models.Chat{ID: id}}},
			{MyChatMember: &models.ChatMemberUpdated{Chat: models.Chat{ID: id}}},
		} {
			if g.updateAllowed(u) != (id == -1001) {
				t.Fatal("allowlist bypass")
			}
		}
	}
}

type restrictionFake struct {
	member            *models.ChatMember
	err               error
	unmuted, unbanned bool
}

func (f *restrictionFake) GetChatMember(context.Context, *tgbot.GetChatMemberParams) (*models.ChatMember, error) {
	return f.member, f.err
}
func (f *restrictionFake) Unmute(context.Context, int64, int64) error { f.unmuted = true; return nil }
func (f *restrictionFake) Unban(context.Context, int64, int64) error  { f.unbanned = true; return nil }
func TestClearMemberRestriction(t *testing.T) {
	f := &restrictionFake{member: &models.ChatMember{Type: models.ChatMemberTypeRestricted}}
	if clearMemberRestriction(context.Background(), f, -1001, 1) != nil || !f.unmuted || f.unbanned {
		t.Fatal("must unmute restricted member")
	}
	f = &restrictionFake{member: &models.ChatMember{}}
	if clearMemberRestriction(context.Background(), f, -1001, 1) != nil || f.unmuted || !f.unbanned {
		t.Fatal("must unban other status")
	}
	f = &restrictionFake{err: errors.New("lookup failed")}
	if clearMemberRestriction(context.Background(), f, -1001, 1) == nil || f.unmuted || f.unbanned {
		t.Fatal("lookup failure must not mutate")
	}
}
