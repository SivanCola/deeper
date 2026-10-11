package session

// IsFramedCodec recognizes the supported v4 wire identities. Historical stores
// retain their codec; new stores use Deepzora's identity and separate directories.
func IsFramedCodec(codec string) bool {
	return codec == V4Codec || codec == "deeper.session.linear/v4" || codec == "reasonix.session.linear/v4"
}

func isPrototypeCodec(codec string) bool {
	return codec == PrototypeCodec || codec == "deeper.session.events/v3" || codec == "reasonix.session.events/v3"
}

// IsLegacyCodec recognizes the supported pre-v4 wire identities.
func IsLegacyCodec(codec string) bool {
	switch codec {
	case FinalV31Codec, LegacyLinearCodec, PrototypeCodec,
		"deeper.session.linear/v3.1", "deeper.session.linear/v3", "deeper.session.events/v3",
		"reasonix.session.linear/v3.1", "reasonix.session.linear/v3", "reasonix.session.events/v3":
		return true
	default:
		return false
	}
}

// Supported reports whether the codec, schema and storage revision agree.
func (manifest Manifest) Supported() bool {
	return supportedStoredManifest(manifest)
}

// IsPreview identifies stores that require the explicit preview import path.
func (manifest Manifest) IsPreview() bool {
	return (manifest.SchemaVersion == 3 && IsLegacyCodec(manifest.Codec)) ||
		(manifest.SchemaVersion == SchemaVersion && IsFramedCodec(manifest.Codec) && manifest.StorageRevision == 0)
}

func validV4RecordCodec(record v4Record, expected string) bool {
	return record.SchemaVersion == V4SchemaVersion && IsFramedCodec(record.Codec) &&
		(expected == "" || record.Codec == expected)
}
