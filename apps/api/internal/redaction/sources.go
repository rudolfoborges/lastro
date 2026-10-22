package redaction

const googleWalletPackage = "com.google.android.apps.walletnfcrel"

func AllowedNotificationPackage(pkg string) bool {
	return pkg == googleWalletPackage
}
