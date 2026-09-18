push rejected twice because gate run writes receipt after sync so push hook sees stale
ledger hash and rejects commit again until agent reruns state sync after staging every
ledger update.
