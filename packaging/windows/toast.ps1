# Zeigt eine Windows-Mitteilung (Toast) des PDH-Terminals. Wird vom Programm als Datei gestartet:
#   powershell.exe -NoProfile -NonInteractive -ExecutionPolicy RemoteSigned -File "<Programmordner>\toast.ps1"
# Der Text kommt Base64-kodiert (UTF-8) in einer Zeile über stdin und landet nur als InnerText im XML;
# er wird nie in ein Skript eingesetzt. Die Mitteilung erscheint nur, wenn der Startmenü-Eintrag mit der
# App-Kennung PDH.Terminal besteht (legt das Setup an).
$ErrorActionPreference = 'Stop'
$t = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String([Console]::In.ReadLine()))
[void][Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime]
[void][Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime]
$x = New-Object Windows.Data.Xml.Dom.XmlDocument
$x.LoadXml('<toast duration="short"><visual><binding template="ToastGeneric"><text>PDH-Terminal</text><text id="2"></text></binding></visual><actions><action content="Terminal öffnen" arguments="open" activationType="foreground"/></actions></toast>')
$x.SelectSingleNode('//text[@id="2"]').InnerText = $t
$n = New-Object Windows.UI.Notifications.ToastNotification $x
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('PDH.Terminal').Show($n)
