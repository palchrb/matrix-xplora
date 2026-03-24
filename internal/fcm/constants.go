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
	// Extract from APK:
	//   apksigner verify --print-certs xplora.apk | grep SHA-1
	// TODO: Fill in after APK extraction.
	XploraAPKCertSHA1 string

	// xploraAppPackage is the Xplora app's package name from AndroidManifest.xml.
	// Extract from APK:
	//   apktool d xplora.apk
	//   cat xplora/AndroidManifest.xml | grep package=
	// Likely "de.roli.watch" or "com.xplora.app" — verify from APK.
	// TODO: Fill in after APK extraction.
	xploraAppPackage string
)

func init() {
	// Values XOR-encoded with key 0x5A (same scheme as Garmin bridge).
	// Encoding: for i, c := range "your-value" { encoded[i] = byte(c) ^ 0x5A }

	// "467048062733" — FCM project_number from google-services.json
	XploraSenderID = decode([]byte{0x6e, 0x6c, 0x6d, 0x6a, 0x6e, 0x62, 0x6a, 0x6c, 0x68, 0x6d, 0x69, 0x69}, xk)

	XploraAPKCertSHA1 = "" // TODO: fill in — apksigner verify --print-certs xplora.apk | grep SHA-1
	xploraAppPackage = ""  // TODO: fill in — grep 'package=' xplora_decompiled/AndroidManifest.xml
}
