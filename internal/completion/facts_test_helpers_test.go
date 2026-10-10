package completion

import "deeper/internal/evidence"

func Build(_ any, ledger *evidence.Ledger) Report { return BuildFacts(ledger, "", nil) }
