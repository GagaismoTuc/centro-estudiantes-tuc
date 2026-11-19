package finance

// SetAfterReportRead makes every later publication of s call hook while it holds its snapshot, after
// reading the Fund report and before reading the Due totals (R6 of #83). nil restores the default.
func SetAfterReportRead(s *Store, hook func()) { s.afterReportRead = hook }
