package session

// IsFramedCodec recognizes the current wire identity.
func IsFramedCodec(codec string) bool {
	return codec == V4Codec
}

// Supported reports whether the codec, schema and storage revision agree.
func (manifest Manifest) Supported() bool {
	return supportedStoredManifest(manifest)
}

func validV4RecordCodec(record v4Record, expected string) bool {
	return record.SchemaVersion == V4SchemaVersion && IsFramedCodec(record.Codec) &&
		(expected == "" || record.Codec == expected)
}
