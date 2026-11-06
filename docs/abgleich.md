# Abgleich Spezifikation und Prüfung

Stand: Abschlussprüfung (Aufgabe 19). Grundlage: [Spezifikation](superpowers/specs/2026-11-05-hmi-kiosk-design.md), Abschnitte 1, 4, 5, 7, 10, 11, 12. Jede Zeile wurde gegen den Code gelesen, nicht nur gegen Testnamen.

Status:

- **abgedeckt**: ein automatischer Test, ein Skript oder eine Auswertung im Code belegt die Anforderung ohne Hardware.
- **nur Hardware**: ohne Raspi nicht prüfbar. Der Punkt steht in der Liste am Ende von [inbetriebnahme.md](inbetriebnahme.md), Abschnitt 12. Hat die Zeile auch einen Test, deckt er nur den Teil ohne Hardware ab.
- **nicht abgedeckt**: keine Prüfung. Jede solche Zeile ist unten in „Nicht abgedeckt“ erklärt.

Abkürzungen der Pfade: `s/` = `internal/session/`, `c/` = `internal/coord/`, `w/` = `internal/web/`, `a/` = `internal/app/`, `st/` = `internal/selftest/`, `h/` = `internal/helper/`, `it/` = `test/integration/`.

## Abschnitt 1: Ziel

| Nr. | Anforderung | Prüfung | Status |
|---|---|---|---|
| 1.1 | Nach Touch steht das HMI-Bild in etwa 1–2 s | `st/steps.go` `client-bild` misst die Zeit bis zum ersten Bild und meldet sie (Test: `st/selftest_test.go` TestRunReportsEverySpecStep); der Wert gilt nur für den Test-VNC-Server, nicht für ein echtes HMI | nur Hardware |
| 1.2 | Bild reagiert flüssiger als mit noVNC | Nicht messbar ohne Gerät und HMI; Eindruck im Betrieb | nur Hardware |
| 1.3 | Nach jedem Sitzungsende (auch Absturz) wieder Kachelseite | `s/manager_test.go` TestClientCrashReturnsToTilesWithNotice, TestTCPLostFor5sStopsClient, TestStartTimeoutStopsClientWithoutEstablished, TestBackDuringStartStopsClientCleanly; Selbstprüfung `rueckkehr` (`st/selftest_test.go` TestReturnStepFollowsTheSessionRules); `it/two_terminals_test.go` | abgedeckt |
| 1.4 | Nach Stromausfall startet das Gerät ohne Eingriff in den Kiosk | Units mit `Restart=always`, `RequiresMountsFor=/data`, Overlay (`packaging/rootfs/etc/systemd/system/`, `packaging/image/verify-image.sh`); Konfiguration atomar (`internal/config/store_test.go` TestSaveLeavesOldFileWhenTargetDirNotWritable, TestLoadCorruptFallsBackToGood); Erststart `packaging/image/test-firstboot.sh`. Der echte Ausfall ist nicht prüfbar | nur Hardware |
| 1.5 | Kein Internetzugang im Betrieb nötig | `w/local_test.go` TestAssetsHaveNoExternalURLs; Admin-Assets eingebettet (`w/embed.go`); Chromium-Update-Suche gedrosselt (`--check-for-update-interval` in `internal/browser/browser.go`, ohne eigenen Test); Installationstest ohne Netz (`test/install/run.sh`) | abgedeckt |
| 1.6 | Nicht-Ziel: `pdh-server` unverändert, keine Integration | Kein Bezug im Code (`grep pdh` findet nur Doku) | abgedeckt |
| 1.7 | Nicht-Ziel: „Nur ansehen“ nur als Client-Einstellung | `internal/client/remmina_test.go` TestRemminaProfileHasExpectedFields (`viewonly`), `client/tigervnc_test.go` TestTigerVNCArgs; Remmina-Abgleich: bei `viewonly=1` kommen keine Eingaben an (Spec 14) | abgedeckt |
| 1.8 | Nicht-Ziel: keine automatische Konfigurationssynchronisierung | Es gibt nur manuellen Export und Import (`w/admin_transfer_test.go`) | abgedeckt |

## Abschnitt 4: Ablauf einer Sitzung

| Nr. | Anforderung | Prüfung | Status |
|---|---|---|---|
| 4.1 | Zustände Kacheln → Anfrage → Rückfrage → Start → Verbunden → Beenden → Kacheln | `s/state.go`; `s/manager_test.go` TestOpenFreeHMIStartsClientAndConnects, TestAskConfirmWaitsThenStartsAfterConfirm, TestCancelFromConfirmReturnsToTiles, TestShutdownStopsClientAndCleansUp | abgedeckt |
| 4.2 | Touch fragt über `coord` die anderen Terminals | `s/flow.go` `request`; `a/runtime_test.go`; `it/two_terminals_test.go` TestTwoTerminalsTakeoverAndLock | abgedeckt |
| 4.3 | Vorabprüfung mit `rfb.Probe`, bei `AuthOK` falsch Hinweis und kein Client | `s/manager_test.go` TestProbeWrongPasswordShowsNoticeNoClient, TestProbeUnreachableShowsNoticeNoClient; `a/runtime_test.go` TestWrongVNCPasswordStartsNoClient; `internal/rfb/probe_test.go` TestProbeWrongPassword | abgedeckt |
| 4.4 | Sitzungsprofil im RAM, Client startet | `internal/client/remmina_test.go` TestRemminaEnvRedirectsXDGIntoSessionDir, TestRemminaWritesPrefSecretMode0600; `s/manager_test.go` TestRAMDirRemovedAfterSessionAndOnStartFailure | abgedeckt |
| 4.5 | Bei ESTABLISHED Kachelseite beenden, Leiste zeigen | `internal/browser/browser_test.go` TestTargetForMapping; `s/manager_test.go` TestOpenFreeHMIStartsClientAndConnects. Das tatsächliche Umschalten der Fenster ist nur am Gerät sichtbar | nur Hardware |
| 4.6 | „Verbindung“ heißt ESTABLISHED (`platform.HasEstablished`), keine neue TCP-Prüfung | `internal/platform/tcpstate_test.go` TestHasEstablishedParsesProcNetTCP, TestHasEstablishedIPv6; Verdrahtung in `a/wiring.go` (DNS-Auflösung getrennt: `a/reach_test.go` TestHostCacheNoDNSInTCP) | abgedeckt |
| 4.7 | Startfrist 15 s ohne ESTABLISHED: Client beenden, Hinweis | `s/manager_test.go` TestStartTimeoutStopsClientWithoutEstablished | abgedeckt |
| 4.8 | Ende durch Zurück | `s/manager_test.go` TestBackDuringStartStopsClientCleanly, `s/hooks_test.go` TestOnEndReportsOperatorBackWithDuration | abgedeckt |
| 4.9 | Ende durch Übernahme | `s/manager_test.go` TestIncomingTakeoverWarns5sThenStops, TestImmediateTakeoverStopsNow | abgedeckt |
| 4.10 | Ende durch Inaktivität | `s/manager_test.go` TestInactivity15MinReturnsToTiles, TestInactivityZeroIsOff, TestInactivityReadFromCurrentConfig | abgedeckt |
| 4.11 | Ende durch Client-Ende | `s/manager_test.go` TestClientCrashReturnsToTilesWithNotice; Selbstprüfung `prozessende` (`st/selftest_test.go` TestProcessEndRecordsHowTheClientEnded) | abgedeckt |
| 4.12 | Ende durch Trennung des HMI (5 s ohne Verbindung, Client dann beenden) | `s/manager_test.go` TestTCPLostFor5sStopsClient, TestTCPReturningResetsLostTimer; `it/two_terminals_test.go` TestPermanentDisplayReconnectsAfterServerRestart | abgedeckt |
| 4.13 | Client beendet, RAM-Daten gelöscht, Kachelseite startet | `s/manager_test.go` TestRAMDirRemovedAfterSessionAndOnStartFailure, TestShutdownStopsClientAndCleansUp; `a/runtime_test.go` TestSessionWritesJournalAndCleansRAMDir | abgedeckt |
| 4.14 | Leiste 48 px oben, Name, Modus, „Zurück“; Bild darunter skaliert | `s/manager_test.go` TestGeometryWithAndWithoutBar; `w/local_test.go` TestBarShowsNameModeAndWarning; `internal/client/remmina_test.go` TestRemminaProfileHasExpectedFields (`scale`, `viewmode`); labwc-Regel `packaging/rootfs/etc/xdg/labwc/rc.xml` (im Container mit openbox gleichwertig geprüft, unter labwc nur am Gerät); Selbstprüfung `fenster-leiste` | nur Hardware |
| 4.15 | Keine unsichtbaren Gesten | `w/assets/kiosk.js` und `bar.html` enthalten keine Gestenbehandlung (Code gelesen); labwc-Bindungen in `rc.xml` ersetzt (keine Kürzel) | abgedeckt |
| 4.16 | Inaktivität: Standard 15 min, 0 = aus, nicht für Daueranzeigen | `config` Default 15 (`internal/config/validate_test.go` TestDefaultIsValid); `s/manager_test.go` TestPermanentDisplayIgnoresInactivity, TestInactivityZeroIsOff | abgedeckt |
| 4.17 | Inaktivität misst nur Touch-Eingaben am Terminal | `internal/platform/touch_test.go` TestWatchTouchCallsActivityOnEvent, TestWatchTouchPicksUpLateDevice; echtes Eingabegerät nur am iiyama | nur Hardware |
| 4.18 | Daueranzeige: Verbindung nach Start, Neuverbinden 2, 4, 8 … höchstens 30 s, Hinweis | `s/manager_test.go` TestPermanentDisplayAutoStartsAfterRun, TestPermanentDisplayBackoff2_4_8_cap30, TestPermanentBackoffResetsAfterLongConnection; `it/two_terminals_test.go` TestPermanentDisplayReconnectsAfterServerRestart | abgedeckt |
| 4.19 | Daueranzeige: Leiste entfällt oder ausblendbar | `internal/browser/browser_test.go` TestTargetForMapping (hideBar); `s/manager_test.go` TestGeometryWithAndWithoutBar; Einstellung `w/admin_test.go` TestClientKioskSettings | abgedeckt |
| 4.20 | Auflösung aus `/sys/class/drm/*/modes`, sonst 1920×1080, in der Admin-Seite überschreibbar | `internal/platform/display_test.go` TestDisplaySizeReadsConnectedOutput, TestDisplaySizeIgnoresDisconnected, TestDisplaySizeFallback1920x1080, TestResolveOverrideWins; `w/admin_system_test.go` TestDisplaySettings. Was der Pi 5 am iiyama meldet, ist nur am Gerät zu sehen | nur Hardware |

## Abschnitt 5: Abstimmung zwischen Terminals

| Nr. | Anforderung | Prüfung | Status |
|---|---|---|---|
| 5.1 | Direkte Kommunikation, feste zufällige Kennung, Name, Rolle | `a/app_test.go` TestFirstStartPersistsTerminalID; `internal/config/validate_test.go` TestValidateRejectsPeerEqualToSelfName; `c/identity_test.go` TestUpdateIdentityChangesPeersAndSelf | abgedeckt |
| 5.2 | Belegung wird alle 2 s gemeldet | `c/coordinator_test.go` TestHeartbeatSentEvery2sWhileHeld, TestTickSendsOnlyForHeldHMIs | abgedeckt |
| 5.3 | Belegung läuft nach 6 s ohne Erneuerung ab, eigene monotone Uhr | `c/lease_test.go` TestLeaseExpiresAfter6sAndRenews; `c/coordinator_test.go` TestPeerCrashFreesHMIAfter6s; `it/two_terminals_test.go` TestPeerKilledHMIFreeAfter6s; Uhr: `c/clock.go` | abgedeckt |
| 5.4 | Anfrage an alle anderen, Zeitlimit 1 s je Terminal | `c/coordinator_test.go` TestRequestTimesOutPerPeerAfter1s, TestRequestDecidesAgainstStrongestHolder | abgedeckt |
| 5.5 | Tabelle: normal will von normal (Übernahme nach Rückfrage / Sperre „belegt durch X“) | `c/decide_test.go` TestDecideTable; `s/manager_test.go` TestRejectedShowsBusyNoticeNoClient, TestAskConfirmWaitsThenStartsAfterConfirm; `it/two_terminals_test.go` TestTwoTerminalsTakeoverAndLock | abgedeckt |
| 5.6 | Tabelle: primär will von normal (Übernahme in beiden Modi) | `c/decide_test.go` TestDecideTable; `it/two_terminals_test.go` TestPrimaryPreemptsNormal | abgedeckt |
| 5.7 | Tabelle: normal will von primär (abgelehnt) | `c/decide_test.go` TestDecideTable; `s/manager_test.go` TestAutomaticStartNeverDisplacesPrimaryHolder | abgedeckt |
| 5.8 | Tabelle: primär will von primär (zuletzt Anfragender / erster behält) | `c/decide_test.go` TestDecideTable | abgedeckt |
| 5.9 | Modus global mit Abweichung je HMI | `c/coordinator_test.go` TestRequestUsesRulesForHMI; `a/runtime_test.go` TestPermanentAndRulesReadCurrentConfig; Eingabe `w/admin_test.go` TestTerminalSettings, TestHMIEditSetsAllFields | abgedeckt |
| 5.10 | Art der Übernahme `rückfrage` (Standard) / `sofort` | `c/decide_test.go` TestDecideTable; `s/manager_test.go` TestAskConfirmWaitsThenStartsAfterConfirm, TestImmediateTakeoverStopsNow | abgedeckt |
| 5.11 | Betroffenes Terminal zeigt 5 s Hinweis, dann Trennung; Primäre überspringen die Rückfrage, Vorwarnung bleibt | `s/manager_test.go` TestIncomingTakeoverWarns5sThenStops, TestTakeOverWaitsWarningBeforeStart; `c/decide_test.go` TestDecideTable | abgedeckt |
| 5.12 | Gleichzeitige Anfragen: höhere Rolle, dann kleinere Kennung, überall gleich | `c/decide_test.go` TestResolveHigherRoleWinsThenSmallerID; `c/coordinator_test.go` TestSimultaneousClaimsAgreeOnOneWinner, TestLateStrongerClaimBeatsFinishedRequest, TestSimultaneousClaimsNoSynchronisation; `s/manager_test.go` TestLostSimultaneousClaimAbortsBeforeClientStart | abgedeckt |
| 5.13 | Terminal nicht erreichbar: Verbindung trotzdem, Hinweis auf Kachel und Leiste | `c/coordinator_test.go` TestRequestAllowsAndReportsUnreachablePeer; `s/manager_test.go` TestUnreachableAndBadKeyAreHintsOnly; `w/local_test.go` TestBarShowsNameModeAndWarning | abgedeckt |
| 5.14 | Daueranzeige gilt für andere als belegt; „Mitschauen erlauben“ (Standard aus) mit `viewonly` | `c/decide_test.go` TestDecideTable; `c/coordinator_test.go` TestHolderPermanentFollowsIsPermanentPerHMI, TestSimultaneousWatchersBothAllowed; `s/manager_test.go` TestAllowWatchForcesViewOnlySpec. Gemeinsamer Zugriff auf ein echtes HMI nur am Gerät | nur Hardware |
| 5.15 | Übernommenes Terminal beendet Sitzung und zeigt, wer übernommen hat | `s/manager_test.go` TestIncomingTakeoverWarns5sThenStops, TestOnEndReportsTakeoverReason; `it/two_terminals_test.go` TestTwoTerminalsTakeoverAndLock | abgedeckt |
| 5.16 | Terminal-Liste in der Admin-Seite gepflegt | `w/admin_test.go` TestTerminalSettings | abgedeckt |

Auslegung: Fordert ein primäres Terminal ein HMI an, das ein anderes Terminal als Daueranzeige hält, gilt der Vorrang (Übernahme mit Vorwarnung). Die Spezifikation nennt diesen Fall nicht; der Automatik-Fall (Daueranzeige verdrängt nie einen primären Halter) ist festgelegt und getestet (TestAutomaticStartNeverDisplacesPrimaryHolder).

## Abschnitt 7: Admin-Seite

| Nr. | Anforderung | Prüfung | Status |
|---|---|---|---|
| 7.1 | HTTPS mit selbst erzeugtem Zertifikat | `w/tlscert_test.go` TestCertCreatedOnceThenReused, TestCertSANsHostnameLocalAndInterfaceIPs, TestCertRegeneratedWhenExpiringWithin30Days; `a/runtime_test.go` TestPeerAndAdminShareTLSPort | abgedeckt |
| 7.2 | Passwort beim ersten Start, Argon2id | `w/admin_auth_test.go` TestFirstStartForcesPasswordSetup, TestLoginWithRealArgon2, TestConcurrentSetupOnlyOneWins | abgedeckt |
| 7.3 | Sperre 5 min nach 5 Fehlversuchen | `w/admin_auth_test.go` TestLockout5FailuresFor5Minutes, TestConcurrentLoginsCannotBypassLockout | abgedeckt |
| 7.4 | HMIs: Name, Adresse, Port, Passwort, Symbol, Farbe, Reihenfolge, „Nur ansehen“, Modus, Übernahme, Mitschauen | `w/admin_test.go` TestHMIEditSetsAllFields, TestSaveHMIEncryptsPassword, TestHMIListSortedByOrder, TestHMIRejectsValuesOutsideClosedSets, TestPasswordNeverReturnedInResponses | abgedeckt |
| 7.5 | Verbindungsprüfung mit Anzeige der Auflösung | `w/admin_test.go` TestProbeShowsResultWithin10Seconds; `a/runtime_test.go` TestAdminProbeDecryptsAndHonoursLimit | abgedeckt |
| 7.6 | Client: Profil, Qualität, Farbtiefe, Skalierung; Hinweis bei TigerVNC | `w/admin_test.go` TestClientKioskSettings; `internal/config/validate.go`; Hinweis in `w/assets/admin/client.html` (gelesen) | abgedeckt |
| 7.7 | Kiosk: Inaktivität, Layout, Daueranzeige und festes HMI | `w/admin_test.go` TestClientKioskSettings; `internal/config/validate_test.go` TestValidateRejectsPermanentHMIUnknown | abgedeckt |
| 7.8 | Terminals: Name, Rolle, andere Terminals, gemeinsamer Schlüssel, Modus global | `w/admin_test.go` TestTerminalSettings, TestSharedKeyGeneratedShownOnce, TestSharedKeyByHand | abgedeckt |
| 7.9 | Netzwerk: WLAN und Bluetooth dauerhaft aus, NTP-Server | `w/admin_system_test.go` TestWifiOffCallsHelperAndMarksRebootForOn, TestBluetoothHasNoWifiGuard, TestNTPValidatedBeforeHelper; `internal/config/ntp_test.go` TestValidNTPServer | abgedeckt |
| 7.10 | System: Protokoll, Schreibschutz-Status, freier Platz, Version | `w/admin_system_test.go` TestLogViewShowsLast200LinesOfNewestFile, TestLogDownloadLimitedTo5MB, TestSystemPageShowsStatusAndSelftest; `a/runtime_test.go` TestStatusReportsOverlayAndVersion | abgedeckt |
| 7.11 | System: Neustart, Uhrzeit setzen | `w/admin_system_test.go` TestRebootAndRestartRequireConfirmation, TestSetTimeValidatedAndSentAsRFC3339; `h/commands_test.go` TestSetTimeRejectsNonRFC3339, TestSetTimeConvertsToLocal. Uhrzeit um die Zeitumstellung nur am Gerät | nur Hardware |
| 7.12 | System: Update hochladen | `w/admin_system_test.go` TestUpdateInstalls; `internal/update/*_test.go`; Rückfall `internal/update/guard_test.go` TestGuardRollsBackWithoutHealthyMarkerAfter60s. Echter Neustart mit Rückfall nur am Gerät | nur Hardware |
| 7.13 | System: Konfiguration sichern und wiederherstellen | `w/admin_transfer_test.go` TestExportFileNameAndContentAreEncrypted, TestImportWrongPasswordLeavesConfigUntouched, TestImportInvalidConfigLeavesConfigUntouched | abgedeckt |
| 7.14 | System: Selbstprüfung, Dauerprüfung | `a/selftest_test.go`, `a/soakjob_test.go`, `w/admin_soak_test.go` | abgedeckt |
| 7.15 | System: Auflösung, Grafikprofil | `w/admin_system_test.go` TestDisplaySettings | abgedeckt |
| 7.16 | Schreibrechte: Admin-Seite schreibt nur auf die Datenpartition; Betriebssystem und Pakete nicht änderbar | Routen in `w/admin.go` gelesen: keine Pfade zu System oder Paketen; Schreiben nur über `config.Save`, Update-Ordner und Protokoll unter `/data`; Bootpartition nur über den Helfer (`h/bootconfig_test.go`) | abgedeckt |
| 7.17 | Nur WLAN und Bluetooth ändern über den Helfer die Bootpartition | `h/commands_test.go` TestWifiOffSequence, TestRadioCommandsMatrix, TestUnchangedConfigStillRemountsRo; `h/bootconfig_test.go` | abgedeckt |
| 7.18 | WLAN: `dtoverlay=disable-wifi` und sofort `rfkill`; Bluetooth `disable-bt`; verweigert bei Admin-Sitzung über WLAN oder ohne Ethernet-Link; Einschalten braucht Neustart | `h/commands_test.go` TestWifiOffSequence; `w/admin_system_test.go` TestWifiOffRefusedWhenRemoteViaWifi, TestWifiOffRefusedWithoutEthernetLink, TestWifiOffCallsHelperAndMarksRebootForOn; Wirkung der Overlay-Namen auf dem Pi 5 nur am Gerät | nur Hardware |
| 7.19 | Export mit eigenem Passwort (Argon2id, AES-GCM); Import behält Name, Kennung, Rolle | `internal/secrets/export_test.go` TestExportRoundTrip, TestExportWrongPasswordFails, TestExportTamperedFails; `w/admin_transfer_test.go` TestExportImportKeepsDeviceIdentity, TestExportRequiresStrongRepeatedPassword | abgedeckt |

## Abschnitt 10: Fehlerbehandlung

| Nr. | Fall | Prüfung | Status |
|---|---|---|---|
| 10.1 | Client stürzt ab: Kachelseite mit Hinweis, Daueranzeige verbindet neu | `s/manager_test.go` TestClientCrashReturnsToTilesWithNotice, TestPermanentDisplayBackoff2_4_8_cap30; Selbstprüfung `rueckkehr` | abgedeckt |
| 10.2 | HMI nicht erreichbar: Kachel zeigt es, TCP-Prüfung alle 10 s | `a/reach_test.go` TestReachabilityBoundedConcurrencyAndLimit, TestReachabilitySkipsHMIInSession (`reachEvery` = 10 s); `a/runtime_test.go` TestTilesShowReachabilityAndOrder; `w/local_test.go` TestTileBusyAndFallbacks | abgedeckt |
| 10.3 | Falsches VNC-Passwort: Vorabprüfung, sonst Startfrist 15 s | `s/manager_test.go` TestProbeWrongPasswordShowsNoticeNoClient, TestStartTimeoutStopsClientWithoutEstablished; Remmina-Dialog nur mit dem echten Client (Container-Abgleich `packaging/remmina-check`, Ergebnis in Spec Abschnitt 14) | abgedeckt |
| 10.4 | Chromium stürzt ab: Neustart mit wachsender Wartezeit, VNC-Sitzung läuft weiter | `internal/browser/browser_test.go` TestCrashRestartsWithBackoff, TestBackoffResetsAfterStableRun, TestMissingBinaryRetriesOnBackoff. Dass die Sitzung weiterläuft, folgt aus dem Aufbau (Supervisor und Sitzung sind getrennt, `a/app.go`); es gibt keinen eigenen Test, der Handgriff steht in der Hardwareliste | nur Hardware |
| 10.5 | Dienst stürzt ab: systemd startet neu, Kindprozesse aufgeräumt | `a/cleanup_test.go` TestKillLeftoversOnlyVerifiedGroups, TestKillLeftoversEscalatesToSigkill; `a/app_test.go` TestStartupKillsLeftoverChildren; `Restart=always` in `hmi-kiosk.service` | abgedeckt |
| 10.6 | System hängt: Hardware-Watchdog startet neu | `a/sdnotify_test.go` TestWatchdogOnlyWhenProbeAnswers, TestWatchdogProbeHasTwoSecondLimit; `WatchdogSec=30s`, `watchdog.conf` (`verify-image.sh`); Selbstprüfung `schutz` (`st/selftest_test.go` TestOverlayAndWatchdogDetection). Ein echter Aussetzer wird nicht erzeugt | nur Hardware |
| 10.7 | Terminal nicht erreichbar | siehe 5.13 | abgedeckt |
| 10.8 | Konfiguration unlesbar: Rückfall auf `.good`, Hinweis in der Admin-Seite | `internal/config/store_test.go` TestLoadCorruptFallsBackToGood, TestLoadMissingFallsBackToGood; `a/app_test.go` TestStartupUsesFallbackConfigAndFlagsWarning; `w/admin_fallback_test.go` TestHashFreeFallbackNeverOpensSetup | abgedeckt |
| 10.9 | Datenpartition defekt oder schreibgeschützt: Kachelseite läuft weiter, Admin meldet den Fehler | `a/app_test.go` TestReadOnlyDataDirStillServesTiles, TestBrokenDataPartsKeepLoadedConfig; `w/admin_test.go` TestSaveFailureKeepsOldConfigAndShowsError; Partition-Verhalten auf echter SD-Karte nur am Gerät (Stromausfall, Hardwareliste) | abgedeckt |
| 10.10 | Monitor nicht gefunden: 1920×1080, Hinweis im Protokoll | `internal/platform/display_test.go` TestDisplaySizeFallback1920x1080; Protokollzeile `a/app.go` („Kein Monitor erkannt …“, ohne eigenen Test), Hinweis in der Admin-Seite `a/status.go` | abgedeckt |
| 10.11 | Uhrzeit falsch: nur die Protokollzeit betroffen | Abstimmung nutzt monotone Uhr und Zufallszahlen (`c/clock.go`, `c/httpapi_test.go` TestNonceExpiresAfter30s); Peer-TLS prüft keine Zertifikatszeit (`c/transport.go`); Admin-Zertifikat wird bei Ablauf neu erzeugt (`w/tlscert_test.go`). Browserwarnung zum Zertifikat bei sehr falscher Zeit nicht ausgeschlossen | abgedeckt |

## Abschnitt 11: Sicherheit

| Nr. | Anforderung | Prüfung | Status |
|---|---|---|---|
| 11.1 | Dienst ohne root | `hmi-kiosk.service` (`User=`), `h/server_test.go`; Image-Prüfung `packaging/image/verify-image.sh`; ExecStartPre unter PAM-Sitzung nur am Gerät | nur Hardware |
| 11.2 | Helfer: feste Befehle, Unix-Socket mit Benutzerprüfung | `h/commands_test.go` TestUnknownCommandRejected; `h/server_test.go` TestPeerCredRejectsOtherUID, TestPeerUIDIsReal, TestMalformedRequests | abgedeckt |
| 11.3 | `set-time`, `set-ntp`, `update-activate` je ein streng geprüfter Wert, andere Befehle keinen | `h/commands_test.go` TestNoArgCommandsRejectArgument, TestSetTimeRejectsNonRFC3339, TestSetNTPRejectsShellChars, TestUpdateActivateRejectsPathTraversal; `h/fix1_test.go` TestVersionTooLongRejected | abgedeckt |
| 11.4 | Terminal-API: HMAC-SHA-256 mit gemeinsamem Schlüssel | `c/httpapi_test.go` TestHandlerRejectsBadSignature, TestHandlerRejectsEverythingWithEmptyKey, TestTransportMaps401ToErrBadKey; `it/two_terminals_test.go` TestWrongSharedKeyAllowsWithNotice | abgedeckt |
| 11.5 | Einmalige Zufallszahl vor ändernden Befehlen | `c/httpapi_test.go` TestNonceIs32RandomBytes, TestHandlerRejectsReusedNonce, TestHandlerRejectsUnknownNonce, TestNonceExpiresAfter30s, TestNonceStoreBoundedOldestEvicted | abgedeckt |
| 11.6 | Kachelseite nur auf `127.0.0.1`, zeigt nie Adressen oder Passwörter | Bindung `a/app.go` (`DefaultLocalAddr`), `a/app_test.go` TestLocalPortBusyIsAnError; `w/local_test.go` TestTilesNeverContainHostOrPassword, TestForeignHostRejectedEverywhere, TestPostRejectedWithForeignOrigin | abgedeckt |
| 11.7 | Chromium: kein Menü, keine Tastenkürzel | `--kiosk`/`--app` (`internal/browser/browser_test.go` TestTilesArgsIncludeKioskAndRAMProfile); labwc-Bindungen ersetzt (`rc.xml`); Chromium-eigene Tastenkürzel gelten bei angeschlossener Tastatur weiter, das Gerät hat keine | nur Hardware |
| 11.8 | Chromium: Navigation nur zur lokalen Adresse | Chromium-Richtlinie `packaging/rootfs/etc/chromium/policies/managed/hmi-kiosk.json` (`URLBlocklist` `["*"]`, `URLAllowlist` `http://127.0.0.1:8080` und `http://localhost:8080`) im Abbild (`stage-hmi/01-hmi/00-run.sh`) und in `install.sh`; `verify-image.sh` Prüfung `chromium-richtlinie`; `test/install/check.sh` (Datei, zwei Läufe gleich). Wirkung mit dem Debian-Paket `chromium` 156 (trixie, headless) in einem Container belegt: Kachelseite auf 127.0.0.1:8080 und localhost:8080 lädt, `https://example.com`, ein anderer lokaler Port und 127.0.0.1:8081 liefern ERR_BLOCKED_BY_ADMINISTRATOR, ohne Richtlinie lädt die fremde Seite | abgedeckt (Wirkung am Gerät in H16) |
| 11.9 | VNC-Verbindung unverschlüsselt im abgeschotteten Netz akzeptiert | Entscheidung, kein Prüfpunkt | abgedeckt |

## Abschnitt 12: Tests

| Nr. | Anforderung | Prüfung | Status |
|---|---|---|---|
| 12.1 | `coord`: Tabelle, gleichzeitige Anfragen, Ablauf, Netzausfall, simulierte Uhr | `c/decide_test.go`, `c/coordinator_test.go`, `c/lease_test.go` (gefälschte Uhr `c/clock.go`); Netzausfall: TestPeerCrashFreesHMIAfter6s, `it/two_terminals_test.go` | abgedeckt |
| 12.2 | `session`: Automat mit gefälschtem Client (Absturz, Hänger, Übernahme während des Starts) | `s/manager_test.go` TestClientCrashReturnsToTilesWithNotice, TestStartTimeoutStopsClientWithoutEstablished, TestLostSimultaneousClaimAbortsBeforeClientStart | abgedeckt |
| 12.3 | `client`: Profil und Verschlüsselung gegen Testwerte | `internal/client/remmina_test.go` TestRemminaEncryptMatchesKnownVector, TestRemminaProfileHasExpectedFields; `tigervnc_test.go` | abgedeckt |
| 12.4 | `client`: echte `remmina`-Version im Container | `packaging/remmina-check/run.sh` und `cmd/hmi-kiosk-remmina-check`, Lauf am 2026-11-05, Ergebnisse in Spec Abschnitt 14 und `docs/remmina-check/`. Läuft nicht in `go test` (braucht Docker und Netz) | abgedeckt |
| 12.5 | `config`: atomares Speichern, beschädigte Datei, Rückfall | `internal/config/store_test.go` TestSaveThenLoadRoundTrips, TestSaveLeavesOldFileWhenTargetDirNotWritable, TestLoadCorruptFallsBackToGood | abgedeckt |
| 12.6 | Integrationstest: mehrere Dienste plus simulierter VNC-Server | `it/two_terminals_test.go` (5 Tests, Build-Tag `integration`). Abweichung: Die Dienste laufen im Testprozess auf amd64, nicht in Containern auf `arm64` per QEMU; die arm64-Binärdateien werden nur gebaut | abgedeckt |
| 12.7 | Selbstprüfung 1: Client zeigt Bild, Skalierung | Schritt `client-bild` (`st/selftest_test.go` TestClientImageChecksWrongPassword); Sichtprüfung des Bildes bleibt (PRÜFEN) | nur Hardware |
| 12.8 | Selbstprüfung 2: Fensterplatz und Leiste | Schritt `fenster-leiste` (TestWindowStepMeasuresOnX11); unter labwc nur Sichtprüfung | nur Hardware |
| 12.9 | Selbstprüfung 3: Touch-Zielpunkte | Schritt `touch` (TestTouchFailsWithoutHits, TestTouchBoard; `w/selftest_local_test.go` TestSelftestTouchHitRules); echter Touch nur am iiyama | nur Hardware |
| 12.10 | Selbstprüfung 4: Zurück, Absturz, Trennung führen zur Kachelseite | Schritt `rueckkehr` (TestReturnStepFollowsTheSessionRules, TestReturnStepToleratesLateServerBookkeeping) | abgedeckt |
| 12.11 | Selbstprüfung 5: Prozessende von Remmina | Schritt `prozessende` (TestProcessEndRecordsHowTheClientEnded, TestWaitDisconnectedGraceIsTwoSecondsRegardlessOfPoll) | abgedeckt |
| 12.12 | Selbstprüfung 6: andere Terminals erreichbar, Schlüssel stimmt | Schritt `terminals` (TestTerminalsReportsEachPeer); `c/check_test.go` TestCheckPeerDistinguishesStates | abgedeckt |
| 12.13 | Selbstprüfung 7: Schreibschutz und Watchdog aktiv | Schritt `schutz` (TestOverlayAndWatchdogDetection, TestProtectionDistinguishesInstallScriptFromImage); Wirkung nur am Gerät | nur Hardware |
| 12.14 | Selbstprüfung als `hmi-kiosk selftest` und in der Admin-Seite | `cmd/hmi-kiosk/main_test.go` TestSelftestWithoutServiceExitsWithTwo; `a/selftest_test.go`; `w/admin_system_test.go` TestSystemPageShowsStatusAndSelftest | abgedeckt |
| 12.15 | Dauerprüfung `hmi-kiosk soak` (Sitzungen wiederholt, 24 h, Speicher) | Logik: `st/soak_test.go` (Zählung, Leck-Erkennung, Freigabe je Zyklus); Start aus der Admin-Seite `w/admin_soak_test.go`. Die 24 Stunden sind nur am Gerät möglich | nur Hardware |

## Nicht abgedeckt

Keine Zeile. Die frühere Lücke 11.8 (Navigation nur zur lokalen Adresse) ist durch die Chromium-Richtlinie geschlossen, siehe die Zeile oben; am Gerät bleibt nur die Sichtprüfung (H16).

Alle übrigen Zeilen sind abgedeckt oder stehen in der Hardwareliste.
