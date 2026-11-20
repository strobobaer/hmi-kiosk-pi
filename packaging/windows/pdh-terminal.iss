; Setup des PDH-Terminals für Windows 11 (Inno Setup 6). Gebaut von packaging/windows/build-setup.sh:
;   iscc /DAppVersion=x.y.z packaging\windows\pdh-terminal.iss
; Erwartet dist\win\pdh-terminal.exe und dist\win\pdh-terminal-start.exe (gleiche Version, gleicher Schlüssel).
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

[Dirs]
; Datenordner (Kopplung, Einstellungen, Updates unter app\): für alle Benutzer les- und schreibbar, bleibt beim Deinstallieren.
Name: "{commonappdata}\PDH-Terminal"; Permissions: users-modify; Flags: uninsneveruninstall

[Registry]
; Autostart für alle Benutzer: der Starter, nicht das Programm.
Root: HKLM; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "PDH-Terminal"; ValueData: """{app}\pdh-terminal-start.exe"""; Flags: uninsdeletevalue

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
Filename: "{app}\{#Starter}"; WorkingDir: "{app}"; Flags: nowait runasoriginaluser; Check: NotVerySilent

[UninstallRun]
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""{#FirewallRule}"""; Flags: runhidden; RunOnceId: "DelFirewallRule"

[Code]
const
  UninstallKey = 'Software\Microsoft\Windows\CurrentVersion\Uninstall\{8B2F6C1E-4D7A-4E53-9A0B-5C3D1F7E2A94}_is1';

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
  oldText: String;
  oldVer, newVer: Int64;
begin
  Result := True;
  // Dieselbe oder höhere Version über eine Installation ist ein Update und behält die Daten.
  // Eine ältere Version würde die neuere überschreiben: abweisen.
  if RegQueryStringValue(HKLM, UninstallKey, 'DisplayVersion', oldText) then
    if StrToVersion(oldText, oldVer) and StrToVersion('{#AppVersion}', newVer) then
      if ComparePackedVersion(oldVer, newVer) > 0 then
      begin
        SuppressibleMsgBox('Es ist schon die neuere Version ' + oldText + ' installiert. Dieses Setup (Version {#AppVersion}) ändert nichts.', mbError, MB_OK, IDOK);
        Result := False;
      end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  // Laufende Programme halten ihre Dateien fest.
  StopTerminal();
  Result := '';
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
    StopTerminal();
  if CurUninstallStep = usPostUninstall then
    if not UninstallSilent then
      MsgBox('Kopplung und Einstellungen bleiben in C:\ProgramData\PDH-Terminal.', mbInformation, MB_OK);
end;
