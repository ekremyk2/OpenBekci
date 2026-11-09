package httpapi

import (
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/chat"
	"github.com/openbekci/openbekci/server/internal/core/notifications"
)

// Matrix rows of the chat channel. Nothing is configured in the matrix
// environment, so starting a link answers 409 and a link that is not the
// caller's answers 404 to every role that holds the permission.
func init() {
	rows := map[string]mrow{
		"operations.listChatLinks":      {perms: perms(notifications.PermRead), status: 200, build: mget("/v1/orgs/{org_id}/me/chat-links")},
		"operations.startChatLink":      {perms: perms(notifications.PermRead), status: 409, build: fixed("POST", "/v1/orgs/{org_id}/me/chat-links/telegram:start", "")},
		"operations.listChatSettings":   {perms: perms(chat.PermManage), status: 200, build: mget("/v1/orgs/{org_id}/chat-settings")},
		"operations.updateChatSettings": {perms: perms(chat.PermManage), status: 200, build: fixed("PUT", "/v1/orgs/{org_id}/chat-settings/telegram", `{"values":{"bot_username":"bekci_bot"}}`)},
		"operations.deleteChatLink": {perms: perms(notifications.PermRead), status: 404, build: func(_ *authztest.World, org authztest.Org, _ *testing.T) mreq {
			return mreq{method: "DELETE", path: orgPath(org, "/me/chat-links/"+uuid.NewString())}
		}},
	}
	// the site's Telegram group: nothing is configured or linked in the matrix environment
	site := func(method, suffix, body string) func(*authztest.World, authztest.Org, *testing.T) mreq {
		return func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: method, path: orgPath(org, "/sites/"+w.NewSite(t, org).Site.String()+suffix), body: body}
		}
	}
	rows["operations.getSiteChatGroup"] = mrow{perms: perms(chat.PermGroupManage), status: 200, build: site("GET", "/chat-group", "")}
	rows["operations.startSiteChatGroupLink"] = mrow{perms: perms(chat.PermGroupManage), status: 409, build: site("POST", "/chat-group:start", "")}
	rows["operations.updateSiteChatGroup"] = mrow{perms: perms(chat.PermGroupManage), status: 200, build: site("PATCH", "/chat-group", `{"whatsapp_invite_url":null}`)}
	rows["operations.unlinkSiteChatGroup"] = mrow{perms: perms(chat.PermGroupManage), status: 404, build: site("DELETE", "/chat-group", "")}
	rows["operations.getSiteChatInfo"] = mrow{perms: perms(notifications.PermRead), status: 200, build: site("GET", "/chat-info", "")}
	for k, v := range rows {
		authzMatrixRows[k] = v
	}
}
