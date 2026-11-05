package web

import "embed"

// assets enthält die Seiten der Kachelseite und der Leiste. Aufgabe 12
// ergänzt assets/admin.
//
//go:embed assets/*
var assets embed.FS
