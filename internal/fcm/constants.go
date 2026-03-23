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
	// google-services.json. Extract from APK:
	//   apktool d xplora.apk
	//   cat xplora/res/raw/google-services.json | grep project_number
	// TODO: Fill in after APK extraction.
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
	// TODO: Replace these placeholder byte slices with XOR-encoded values
	// after extracting the constants from the Xplora APK.
	// Use the decode() function with key 0x5A, same as Garmin bridge.
	// Example encoding:
	//   for i, c := range "your-value" { encoded[i] = byte(c) ^ 0x5A }
	XploraSenderID = ""    // TODO: encode and set
	XploraAPKCertSHA1 = "" // TODO: encode and set
	xploraAppPackage = ""  // TODO: encode and set
}
