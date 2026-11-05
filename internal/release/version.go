package release

// Version ist die laufende Programmversion, beim Bau gesetzt per
// -ldflags "-X hmikiosk/internal/release.Version=<version>". Sie muss der
// Version des Update-Pakets gleichen, denn der Update-Wächter vergleicht sie.
var Version = "dev"
