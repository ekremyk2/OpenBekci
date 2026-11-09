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
	for k, v := range rows {
		authzMatrixRows[k] = v
	}
}
