; Setup des PDH-Terminals für Windows 11 (Inno Setup 6). Gebaut von packaging/windows/build-setup.sh:
;   iscc /DAppVersion=x.y.z packaging\windows\pdh-terminal.iss
; Erwartet dist\win\pdh-terminal.exe, pdh-terminal-start.exe und pdh-terminal-kiosk.exe (gleiche Version, gleicher Schlüssel).
;
; Betriebsart: "Zusatz" (Vorgabe, Autostart über HKLM Run) oder "Kiosk" (eigener Benutzer PDH-Kiosk mit Autologon, der
; Starter ist dessen Shell, kein Run-Eintrag). Still: /MODE=zusatz|kiosk; Neustart nach Kiosk still nur mit /RESTART=yes.
; Die Kiosk-Einrichtung macht pdh-terminal-kiosk.exe (-setup / -undo / -acl; Protokoll log\kiosk-setup.log).
;
; Der Autostart und das Startmenü starten immer den Starter (pdh-terminal-start.exe), nie das Programm selbst:
; Nach einem Update beendet sich das Programm mit Code 4, und nur der Starter startet es (in der neuen Version) neu.

#ifndef AppVersion
  #error AppVersion fehlt: iscc /DAppVersion=x.y.z
#endif
#ifndef BinDir
  #define BinDir "..\..\dist\win"
#endif
#define AppName "PDH-Terminal"
#define Starter "pdh-terminal-start.exe"
#define FirewallRule "PDH-Terminal Admin"
#define KioskHelper "pdh-terminal-kiosk.exe"
#define ModeKey "SOFTWARE\PDH-Terminal"

[Setup]
; Feste Kennung: Ein Setup mit derselben Kennung ist ein Update über die vorhandene Installation.
AppId={{8B2F6C1E-4D7A-4E53-9A0B-5C3D1F7E2A94}
AppName={#AppName}
AppVersion={#AppVersion}
VersionInfoVersion={#AppVersion}
AppPublisher=PDH
DefaultDirName={autopf}\PDH-Terminal
DefaultGroupName=PDH-Terminal
DisableProgramGroupPage=yes
; Für alle Benutzer: Programm unter Programme, Autostart und Startmenü für alle.
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir=..\..\dist
OutputBaseFilename=PDH-Terminal-Setup-{#AppVersion}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
UninstallDisplayIcon={app}\{#Starter}
UninstallDisplayName={#AppName}

[Languages]
Name: "de"; MessagesFile: "compiler:Languages\German.isl"

[Files]
Source: "{#BinDir}\pdh-terminal.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#BinDir}\pdh-terminal-start.exe"; DestDir: "{app}"; Flags: ignoreversion
; Kiosk-Helfer: richtet den Kioskbetrieb ein (-setup), nimmt ihn zurück (-undo) und setzt die Rechte des Datenordners neu (-acl); läuft nur aus dem Setup.
Source: "{#BinDir}\pdh-terminal-kiosk.exe"; DestDir: "{app}"; Flags: ignoreversion
; Skript der Windows-Mitteilung (Toast): fest, ohne Text; das Programm startet es mit -File aus diesem Ordner.
Source: "toast.ps1"; DestDir: "{app}"; Flags: ignoreversion

[Dirs]
; Datenordner (Kopplung, Einstellungen, Updates unter app\), bleibt beim Deinstallieren. Zusatz: für alle Benutzer les- und
; schreibbar. Kiosk: keine Rechte hier (users-modify würde die Rechte des Kiosks aufweichen: nur Administratoren, SYSTEM und
; PDH-Kiosk); der Helfer (-setup / -acl) setzt sie.
Name: "{commonappdata}\PDH-Terminal"; Permissions: users-modify; Flags: uninsneveruninstall; Check: ModeZusatz
Name: "{commonappdata}\PDH-Terminal"; Flags: uninsneveruninstall; Check: ModeKiosk

[Registry]
; Edge ohne Erststart-Assistent (Edge-Richtlinie HideFirstRunExperience, nur dieser eine Wert). createvalueifdoesntexist:
; Ein vorhandener Wert eines Administrators bleibt unberührt, und nur ein vom Setup angelegter Wert wird bei der
; Deinstallation entfernt. Eine Richtlinie für lokale Netzzugriffe oder unter HKCU gibt es bewusst nicht.
Root: HKLM; Subkey: "SOFTWARE\Policies\Microsoft\Edge"; ValueType: dword; ValueName: "HideFirstRunExperience"; ValueData: 1; Flags: uninsdeletevalue createvalueifdoesntexist
; Autostart für alle Benutzer: der Starter, nicht das Programm. Im Kiosk entfällt er (der Starter ist dort die Shell des
; Benutzers PDH-Kiosk); [Code] entfernt einen vorhandenen Eintrag und legt ihn beim Fehlschlag der Kiosk-Einrichtung wieder an.
Root: HKLM; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "PDH-Terminal"; ValueData: """{app}\pdh-terminal-start.exe"""; Flags: uninsdeletevalue; Check: WantRunKey

[Icons]
; Die Verknüpfung mit AppUserModelID braucht Windows für die Benachrichtigungen (Toast).
Name: "{commonprograms}\PDH-Terminal"; Filename: "{app}\pdh-terminal-start.exe"; WorkingDir: "{app}"; AppUserModelID: "PDH.Terminal"

[INI]
Filename: "{commonprograms}\PDH-Terminal Admin.url"; Section: "InternetShortcut"; Key: "URL"; String: "https://127.0.0.1:8443/admin/"

[UninstallDelete]
Type: files; Name: "{commonprograms}\PDH-Terminal Admin.url"

[Run]
; Firewall: Admin-Seite aus dem Firmen- und Heimnetz (nicht aus öffentlichen Netzen). Erst löschen, damit ein Update keine Doppelung anlegt.
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""{#FirewallRule}"""; Flags: runhidden; StatusMsg: "Firewall-Regel wird erneuert ..."
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall add rule name=""{#FirewallRule}"" dir=in action=allow protocol=TCP localport=8443 profile=domain,private"; Flags: runhidden; StatusMsg: "Firewall-Regel wird angelegt ..."
; Nach der Installation startet der Starter im Benutzerkonto des Installierenden, außer bei /VERYSILENT.
Filename: "{app}\{#Starter}"; WorkingDir: "{app}"; Flags: nowait runasoriginaluser; Check: StartAfterInstall

[UninstallRun]
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""{#FirewallRule}"""; Flags: runhidden; RunOnceId: "DelFirewallRule"

[Code]
const
  UninstallKey = 'Software\Microsoft\Windows\CurrentVersion\Uninstall\{8B2F6C1E-4D7A-4E53-9A0B-5C3D1F7E2A94}_is1';
  RunKey = 'Software\Microsoft\Windows\CurrentVersion\Run';
  // Windows Installer: UpgradeCode des MSI (packaging/windows/msi/pdh-terminal.wxs), in der Registrierung verschlüsselt abgelegt.
  MsiUpgradeKey = 'SOFTWARE\Classes\Installer\UpgradeCodes\A60D1944EC37E0041A7FEAA15493BD91';

var
  ModePage: TInputOptionWizardPage;
  KioskWanted: Boolean;   // gewählte Betriebsart: Kiosk
  PrevKiosk: Boolean;     // das Setup hat zuvor Kiosk eingerichtet
  KioskActive: Boolean;   // Kiosk-Einrichtung in diesem Lauf gelungen
  KioskUpdated: Boolean;  // Update eines Kiosks (Kiosk bleibt Kiosk): das Setup hat die Kiosk-Shell beendet
  EditionOk: Boolean;
  UndoFailed: Boolean;    // Deinstallation: -undo gescheitert, der Marker bleibt kiosk

// WizardSilent gilt für /SILENT und /VERYSILENT; hier zählt nur /VERYSILENT (stille Verteilung ohne Programmstart).
function NotVerySilent(): Boolean;
var
  i: Integer;
begin
  Result := True;
  for i := 1 to ParamCount do
    if CompareText(ParamStr(i), '/VERYSILENT') = 0 then
      Result := False;
end;

// Prüffunktionen für [Dirs] (Check nimmt keine Variablen, nur Funktionen).
function ModeZusatz(): Boolean;
begin
  Result := not KioskWanted;
end;

function ModeKiosk(): Boolean;
begin
  Result := KioskWanted;
end;

// Autostart über Run nur in der Betriebsart Zusatz.
function WantRunKey(): Boolean;
begin
  Result := not KioskWanted;
end;

// Im Kiosk startet die Shell des Kiosk-Benutzers das Terminal (nach dem Neustart), nicht das Setup.
function StartAfterInstall(): Boolean;
begin
  Result := NotVerySilent() and not KioskWanted;
end;

// Kiosk braucht Windows Pro, Enterprise (auch IoT Enterprise) oder Education (EditionID: Professional*, Enterprise*,
// IoTEnterprise*, Education*).
function KioskEditionSupported(): Boolean;
var
  ed: String;
begin
  Result := False;
  if RegQueryStringValue(HKLM, 'SOFTWARE\Microsoft\Windows NT\CurrentVersion', 'EditionID', ed) then
    Result := (Pos('Professional', ed) = 1) or (Pos('Enterprise', ed) = 1) or
      (Pos('IoTEnterprise', ed) = 1) or (Pos('Education', ed) = 1);
end;

function HelperPath(): String;
begin
  Result := ExpandConstant('{app}\{#KioskHelper}');
end;

function KioskLogPath(): String;
begin
  Result := ExpandConstant('{commonappdata}\PDH-Terminal\log\kiosk-setup.log');
end;

// Führt den Kiosk-Helfer verborgen aus und gibt seinen Exitcode zurück (-1: nicht startbar).
function RunHelper(const args: String): Integer;
var
  rc: Integer;
begin
  Result := -1;
  if not FileExists(HelperPath()) then
    Exit;
  if Exec(HelperPath(), args, '', SW_HIDE, ewWaitUntilTerminated, rc) then
    Result := rc;
end;

procedure SaveMode(const m: String);
begin
  RegWriteStringValue(HKLM, '{#ModeKey}', 'Betriebsart', m);
end;

// Stellt für den Datenordner "Benutzer: Ändern" wieder her (Zusatz). Nötig, wenn die Kiosk-Einrichtung scheitert, bevor
// sie die Rechte angefasst hat: [Dirs] hat im Kiosk keine Rechte gesetzt, und Zusatz braucht sie.
procedure GrantUsersModify();
var
  rc: Integer;
begin
  Exec(ExpandConstant('{sys}\icacls.exe'), '"' + ExpandConstant('{commonappdata}\PDH-Terminal') + '" /grant *S-1-5-32-545:(OI)(CI)M', '', SW_HIDE, ewWaitUntilTerminated, rc);
  Log('Datenordner: Benutzer Ändern gesetzt, Code=' + IntToStr(rc));
end;

// Setzt die Rechte des Datenordners für den Kiosk neu (Helfer -acl); meldet einen Fehlschlag.
procedure ReapplyKioskACL();
var
  rc: Integer;
  msg: String;
begin
  rc := RunHelper('-acl');
  Log('Kiosk-Rechte des Datenordners: Code=' + IntToStr(rc));
  if rc <> 0 then
  begin
    msg := 'Die Rechte des Datenordners ließen sich für den Kiosk nicht neu setzen (Code ' + IntToStr(rc) + '). Das Setup erneut ausführen oder pdh-terminal-kiosk.exe -acl als Administrator starten. Einzelheiten: ' + KioskLogPath();
    Log(msg);
    SuppressibleMsgBox(msg, mbError, MB_OK, IDOK);
  end;
end;

procedure StopTerminal();
var
  rc: Integer;
begin
  // Nur diese beiden Programme, samt ihren Kindprozessen (der Edge des Terminals). Erst der Starter, damit er nichts neu startet.
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/IM pdh-terminal-start.exe /T /F', '', SW_HIDE, ewWaitUntilTerminated, rc);
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/IM pdh-terminal.exe /T /F', '', SW_HIDE, ewWaitUntilTerminated, rc);
end;

function InitializeSetup(): Boolean;
var
  oldText, prev, m: String;
  oldVer, newVer: Int64;
  msiNames: TArrayOfString;
begin
  Result := True;
  // MSI und Setup schliessen sich aus (zwei Installationen mit gleichen Pfaden und Einträgen).
  // Der Schlüssel zählt nur mit einem Wert darunter (ein Wert je installierter ProductCode).
  if RegGetValueNames(HKLM64, MsiUpgradeKey, msiNames) and (GetArrayLength(msiNames) > 0) then
  begin
    SuppressibleMsgBox('Das PDH-Terminal ist schon als MSI installiert (Verteilung über Intune oder Gruppenrichtlinie). MSI und Setup schließen sich aus: Zuerst das PDH-Terminal unter Apps (Installierte Apps) deinstallieren, dann dieses Setup ausführen.', mbError, MB_OK, IDOK);
    Result := False;
    Exit;
  end;
  // Dieselbe oder höhere Version über eine Installation ist ein Update und behält die Daten.
  // Eine ältere Version würde die neuere überschreiben: abweisen.
  if RegQueryStringValue(HKLM, UninstallKey, 'DisplayVersion', oldText) then
    if StrToVersion(oldText, oldVer) and StrToVersion('{#AppVersion}', newVer) then
      if ComparePackedVersion(oldVer, newVer) > 0 then
      begin
        SuppressibleMsgBox('Es ist schon die neuere Version ' + oldText + ' installiert. Dieses Setup (Version {#AppVersion}) ändert nichts.', mbError, MB_OK, IDOK);
        Result := False;
        Exit;
      end;

  // Betriebsart: /MODE=zusatz|kiosk. Ohne Angabe bleibt es bei der bisherigen (ein stilles Update ändert sie nicht).
  PrevKiosk := RegQueryStringValue(HKLM, '{#ModeKey}', 'Betriebsart', prev) and (CompareText(prev, 'kiosk') = 0);
  EditionOk := KioskEditionSupported();
  m := ExpandConstant('{param:MODE|}');
  if m = '' then
    KioskWanted := PrevKiosk and EditionOk
  else if CompareText(m, 'kiosk') = 0 then
    KioskWanted := True
  else if CompareText(m, 'zusatz') = 0 then
    KioskWanted := False
  else
  begin
    SuppressibleMsgBox('Unbekannte Betriebsart /MODE=' + m + '. Erlaubt: zusatz oder kiosk.', mbError, MB_OK, IDOK);
    Result := False;
    Exit;
  end;
  if KioskWanted and not EditionOk then
  begin
    // Mit /MODE=kiosk auf einer nicht unterstützten Edition: Abbruch mit Fehlercode (still ohne Fenster).
    SuppressibleMsgBox('Der Kioskbetrieb braucht Windows 11 Pro, Enterprise oder Education.', mbError, MB_OK, IDOK);
    Result := False;
  end;
end;

procedure InitializeWizard();
begin
  ModePage := CreateInputOptionPage(wpWelcome, 'Betriebsart', 'Wie wird dieser PC verwendet?',
    'Wählen Sie die Betriebsart. Sie lässt sich später durch ein erneutes Setup wechseln.', True, False);
  ModePage.Add('Zusatz – neben normaler Arbeit');
  ModePage.Add('Kiosk – dieser PC zeigt nur das Terminal');
  if not EditionOk then
  begin
    ModePage.CheckListBox.ItemEnabled[1] := False;
    ModePage.CheckListBox.ItemCaption[1] := 'Kiosk – nicht verfügbar: Der Kioskbetrieb braucht Windows 11 Pro, Enterprise oder Education.';
  end;
  ModePage.SelectedValueIndex := 0;
  if KioskWanted then
    ModePage.SelectedValueIndex := 1;
end;

function NextButtonClick(CurPageID: Integer): Boolean;
begin
  Result := True;
  if CurPageID = ModePage.ID then
    KioskWanted := (ModePage.SelectedValueIndex = 1) and EditionOk;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  // Laufende Programme halten ihre Dateien fest.
  StopTerminal();
  Result := '';
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  rc: Integer;
  msg: String;
begin
  if CurStep = ssInstall then
  begin
    // Kiosk: kein Autostart über Run (die Shell startet das Terminal). In Zusatz legt der [Registry]-Eintrag ihn an.
    if KioskWanted then
      RegDeleteValue(HKLM, RunKey, 'PDH-Terminal');
  end;
  if CurStep = ssPostInstall then
  begin
    KioskActive := False;
    if KioskWanted and PrevKiosk then
    begin
      // Update eines Kiosks: Der Helfer läuft nicht erneut (-setup räumt erst auf und würde Benutzer und Profil löschen,
      // während die Sitzung abgemeldet wird). Der Shell-Pfad {app}\pdh-terminal-start.exe -kiosk bleibt gleich.
      SaveMode('kiosk');
      KioskUpdated := True;
      // Die Rechte des Datenordners bleiben die des Kiosks (nicht "Benutzer: Ändern"): sicherheitshalber neu setzen.
      ReapplyKioskACL();
    end
    else if KioskWanted then
    begin
      // Zusatz nach Kiosk oder Neuinstallation: Der Helfer macht zuerst -undo (wiederholbar) und rollt bei einem Fehler selbst zurück.
      rc := RunHelper('-setup -app "' + ExpandConstant('{app}') + '"');
      if rc = 0 then
      begin
        KioskActive := True;
        SaveMode('kiosk');
      end
      else
      begin
        // Es bleibt bei Zusatz: Autostart wieder an, Setup nicht abbrechen.
        KioskWanted := False;
        GrantUsersModify();
        RegWriteStringValue(HKLM, RunKey, 'PDH-Terminal', '"' + ExpandConstant('{app}\{#Starter}') + '"');
        SaveMode('zusatz');
        if rc = 2 then
          msg := 'Der Kioskbetrieb braucht Windows 11 Pro, Enterprise oder Education. Der PC bleibt in der Betriebsart Zusatz.'
        else
          msg := 'Die Kiosk-Einrichtung ist fehlgeschlagen (Code ' + IntToStr(rc) + '). Der PC bleibt in der Betriebsart Zusatz. Einzelheiten: ' + KioskLogPath();
        Log(msg);
        SuppressibleMsgBox(msg, mbError, MB_OK, IDOK);
      end;
    end
    else
    begin
      // Zusatz nach Kiosk: erst zurückbauen (Benutzer, Autologon, Shell, Rechte des Datenordners).
      if PrevKiosk then
      begin
        rc := RunHelper('-undo');
        if rc <> 0 then
        begin
          // Rückbau gescheitert: Der Kiosk ist noch eingerichtet (die Shell startet das Terminal). Marker bleibt kiosk,
          // damit ein erneutes Setup den Rückbau wiederholt; kein Run-Eintrag (sonst startet das Terminal doppelt).
          RegDeleteValue(HKLM, RunKey, 'PDH-Terminal');
          SaveMode('kiosk');
          // [Dirs] hat in diesem Lauf "Benutzer: Ändern" gesetzt (Zielbetriebsart Zusatz), der Kiosk besteht aber noch.
          ReapplyKioskACL();
          if rc < 0 then
            msg := 'Der Rückbau des Kiosks ist fehlgeschlagen: Der Kiosk-Helfer ließ sich nicht starten. Der PC bleibt in der Betriebsart Kiosk; das Setup erneut mit /MODE=zusatz ausführen.'
          else
            msg := 'Der Rückbau des Kiosks ist fehlgeschlagen (Code ' + IntToStr(rc) + '). Der PC bleibt in der Betriebsart Kiosk; das Setup erneut mit /MODE=zusatz ausführen. Einzelheiten: ' + KioskLogPath();
          Log(msg);
          SuppressibleMsgBox(msg, mbError, MB_OK, IDOK);
        end
        else
          SaveMode('zusatz');
      end
      else
        SaveMode('zusatz');
    end;
  end;
end;

// Nach gelungener Kiosk-Einrichtung und nach dem Update eines Kiosks neu starten (das Update beendet die Kiosk-Shell,
// der Bildschirm bliebe sonst leer): interaktiv als Angebot, still nur mit /RESTART=yes.
function NeedRestart(): Boolean;
begin
  Result := False;
  if KioskActive or KioskUpdated then
  begin
    if WizardSilent then
      Result := CompareText(ExpandConstant('{param:RESTART|}'), 'yes') = 0
    else
      Result := True;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  rc: Integer;
  ok: Boolean;
begin
  if CurUninstallStep = usUninstall then
  begin
    // Erst die Programme beenden, dann den Kiosk zurücknehmen, solange der Helfer noch da ist
    // (-undo ist unschädlich, wenn nichts eingerichtet ist). Die Deinstallation läuft bei einem Fehler weiter.
    StopTerminal();
    rc := -1;
    ok := FileExists(ExpandConstant('{app}\{#KioskHelper}')) and
      Exec(ExpandConstant('{app}\{#KioskHelper}'), '-undo', '', SW_HIDE, ewWaitUntilTerminated, rc);
    Log('Kiosk-Rueckbau bei der Deinstallation: gestartet=' + IntToStr(Ord(ok)) + ', Code=' + IntToStr(rc));
    UndoFailed := (not ok) or (rc <> 0);
    if UndoFailed and not UninstallSilent then
      MsgBox('Der Rückbau des Kiosks ist nicht gelungen (Code ' + IntToStr(rc) + '). Der Benutzer PDH-Kiosk, der Autologon oder die Shell-Einstellung können bestehen bleiben. Zum Entfernen die Installation erneut aufspielen und mit pdh-terminal-kiosk.exe -undo zurückbauen oder die Schritte aus docs/betrieb.md (Betriebsart Kiosk, Rückbau) von Hand ausführen. Einzelheiten: ' + KioskLogPath(), mbError, MB_OK);
    RegDeleteValue(HKLM, RunKey, 'PDH-Terminal');
  end;
  if CurUninstallStep = usPostUninstall then
  begin
    // Nach gescheitertem Rückbau bleibt der Marker (kiosk) stehen, damit ein späteres Setup den Rückbau wiederholt.
    if not UndoFailed then
    begin
      RegDeleteValue(HKLM, '{#ModeKey}', 'Betriebsart');
      RegDeleteKeyIfEmpty(HKLM, '{#ModeKey}');
    end;
    if not UninstallSilent then
      MsgBox('Kopplung und Einstellungen bleiben in C:\ProgramData\PDH-Terminal.', mbInformation, MB_OK);
  end;
end;
