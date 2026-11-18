package elections

// SetBallotFault makes every later vote of s fail with fault's error after its Voter registry row and
// its Ballot were written, before the transaction commits (S9). nil restores the normal behavior.
func SetBallotFault(s *Store, fault func() error) { s.ballotFault = fault }
