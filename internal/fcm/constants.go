package fcm

func decode(data []byte, key byte) string {
	out := make([]byte, len(data))
	for i, b := range data {
		out[i] = b ^ key
	}
	return string(out)
}

const xk = 0x5A

var (
	// XploraSenderID is the FCM project_number (sender ID) from the Xplora app's
	// google-services.json (project: xplora-app-commercial).
	// Value: 467048062733
	XploraSenderID string

	// XploraAPKCertSHA1 is the SHA1 of the Xplora app's APK signing certificate.
	// Value: 45010bc350ad27866509cb5dfc7d9deb3eff4a2a
	XploraAPKCertSHA1 string

	// xploraAppPackage is the Xplora app's package name from AndroidManifest.xml.
	// Value: com.xplora.xplorav2
	xploraAppPackage string
)

func init() {
	// Values XOR-encoded with key 0x5A (same scheme as Garmin bridge).
	// Encoding: for i, c := range "your-value" { encoded[i] = byte(c) ^ 0x5A }

	// "467048062733" — FCM project_number from google-services.json
	XploraSenderID = decode([]byte{0x6e, 0x6c, 0x6d, 0x6a, 0x6e, 0x62, 0x6a, 0x6c, 0x68, 0x6d, 0x69, 0x69}, xk)

	// "45010bc350ad27866509cb5dfc7d9deb3eff4a2a" — Signer #1 SHA-1 from apksigner
	XploraAPKCertSHA1 = decode([]byte{0x6e, 0x6f, 0x6a, 0x6b, 0x6a, 0x38, 0x39, 0x69, 0x6f, 0x6a, 0x3b, 0x3e, 0x68, 0x6d, 0x62, 0x6c, 0x6c, 0x6f, 0x6a, 0x63, 0x39, 0x38, 0x6f, 0x3e, 0x3c, 0x39, 0x6d, 0x3e, 0x63, 0x3e, 0x3f, 0x38, 0x69, 0x3f, 0x3c, 0x3c, 0x6e, 0x3b, 0x68, 0x3b}, xk)

	// "com.xplora.xplorav2" — package name from AndroidManifest.xml
	xploraAppPackage = decode([]byte{0x39, 0x35, 0x37, 0x74, 0x22, 0x2a, 0x36, 0x35, 0x28, 0x3b, 0x74, 0x22, 0x2a, 0x36, 0x35, 0x28, 0x3b, 0x2c, 0x68}, xk)
}
