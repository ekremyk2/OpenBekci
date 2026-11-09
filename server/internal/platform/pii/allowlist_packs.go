package pii

func init() {
	const why = "a list of code-defined pack contributions (document templates, address field definitions), not personal data"
	Allowlist["packs.Contributions.Templates"] = why
	Allowlist["packs.Contributions.AddressFormats"] = why
}
