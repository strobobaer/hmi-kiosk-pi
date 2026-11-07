package helper

import "strconv"

// partName leitet den Partitionsnamen aus dem Datenträgernamen ab: endet dieser auf eine Ziffer
// (mmcblk0, nvme0n1), kommt ein "p" davor, sonst nicht (sda).
func partName(disk string, n int) string {
	sep := ""
	if disk != "" && disk[len(disk)-1] >= '0' && disk[len(disk)-1] <= '9' {
		sep = "p"
	}
	return disk + sep + strconv.Itoa(n)
}
