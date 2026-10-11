package completion

import "deepzora/internal/evidence"

func Build(_ any, ledger *evidence.Ledger) Report { return BuildFacts(ledger, "", nil) }
