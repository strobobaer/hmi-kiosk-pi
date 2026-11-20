// Package winsignal ist der Weg, auf dem ein zweiter Start des PDH-Terminals (Startmenü, Benachrichtigung)
// das laufende Terminal derselben Sitzung bittet, sein Fenster zu zeigen. Unter Windows ist es ein benanntes
// Ereignis im Namensraum der Sitzung (Local\); eine andere Sitzung findet es nicht.
package winsignal

// OpenName ist das Ereignis „Terminal öffnen“ dieser Sitzung.
const OpenName = `Local\PDH-Terminal-Open`
