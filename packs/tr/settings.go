// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"encoding/json"

	"github.com/openbekci/openbekci/sdk"
)

// settings is the pack's settings schema. Only tr_kvkk_data_controller is used
// in milestone TR-1; the iyzico, İleti Merkezi and bank-format settings are
// declared here so that the schema is stable, and are implemented in TR-2.
// Secret fields are top-level properties (the registry finds x-secret only
// there), so the nested objects of the plan's sketch are flattened:
// tr.payments.iyzico becomes tr_payments_iyzico_<field>.
type settings struct{}

const iyzicoProps = `
    "tr_payments_iyzico_enabled":    {"type": "boolean", "x-ui": {"order": 10, "group": "iyzico", "help": "tr.settings.iyzico"}},
    "tr_payments_iyzico_api_key":    {"type": "string", "x-secret": true, "x-ui": {"order": 11, "group": "iyzico", "widget": "password"}},
    "tr_payments_iyzico_secret_key": {"type": "string", "x-secret": true, "x-ui": {"order": 12, "group": "iyzico", "widget": "password"}},
    "tr_payments_iyzico_sandbox":    {"type": "boolean", "x-ui": {"order": 13, "group": "iyzico"}},
    "tr_sms_iletimerkezi_enabled":   {"type": "boolean", "x-ui": {"order": 20, "group": "iletimerkezi", "help": "tr.settings.iletimerkezi"}},
    "tr_sms_iletimerkezi_username":  {"type": "string", "x-ui": {"order": 21, "group": "iletimerkezi"}},
    "tr_sms_iletimerkezi_password":  {"type": "string", "x-secret": true, "x-ui": {"order": 22, "group": "iletimerkezi", "widget": "password"}},
    "tr_sms_iletimerkezi_sender_id": {"type": "string", "maxLength": 11, "x-ui": {"order": 23, "group": "iletimerkezi"}}`

const kvkkProp = `
    "tr_kvkk_data_controller": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "legal_name": {"type": "string", "maxLength": 200},
        "address":    {"type": "string", "maxLength": 500},
        "contact":    {"type": "string", "maxLength": 200},
        "verbis_no":  {"type": "string", "maxLength": 50}
      },
      "x-ui": {"order": 1, "group": "kvkk", "help": "tr.settings.kvkk_data_controller"}
    }`

const banksProp = `
    "tr_bank_formats": {"type": "array", "items": {"type": "string"}, "uniqueItems": true, "x-ui": {"order": 30, "group": "bank", "help": "tr.settings.bank_formats"}}`

var orgSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {` + iyzicoProps + `,` + banksProp + `
  }
}`)

var siteSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {` + kvkkProp + `,` + iyzicoProps + `
  }
}`)

// Schema implements sdk.SettingsSchema.
func (settings) Schema(scope sdk.ScopeKind) json.RawMessage {
	switch scope {
	case sdk.ScopeOrg:
		return orgSchema
	case sdk.ScopeSite:
		return siteSchema
	}
	return nil
}
