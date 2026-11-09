// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import "github.com/openbekci/openbekci/sdk"

// address is the Turkish address form as data: street line, ilçe and il from
// the reference data (cascading selects), the five-digit posta kodu and the
// optional ten-digit UAVT address code (Ulusal Adres Veri Tabanı).
type address struct{}

func (address) ID() string      { return ID + ".address" }
func (address) Country() string { return "TR" }

func (address) Fields() []sdk.AddressField {
	return []sdk.AddressField{
		{Key: "line1", LabelKey: ID + ".address.line1", Required: true},
		{Key: "line2", LabelKey: ID + ".address.line2"},
		{Key: "province", LabelKey: ID + ".address.province", Required: true, Reference: "province"},
		{Key: "district", LabelKey: ID + ".address.district", Required: true, Reference: "district"},
		{Key: "postal_code", LabelKey: ID + ".address.postal_code", Pattern: `^[0-9]{5}$`},
		{Key: "uavt", LabelKey: ID + ".address.uavt", Pattern: `^[0-9]{10}$`},
	}
}
