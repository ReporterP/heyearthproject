-- QA 2026-11-18 (Beads heyearthproject-jzd5.13): an admin's wallet reason is internal — it lives
-- in the audit log (wallet.credit / wallet.debit, with wallet_tx_id) — and
-- wallet_transactions.comment of admin rows now holds only an optional text written for the
-- client. Rows made before this hold the internal reason there, which the client would now see
-- as the entry's title: clear it, so they read «Начисление от HiEarth» / «Списание HiEarth».
UPDATE wallet_transactions SET comment = '' WHERE kind IN ('admin_credit', 'admin_debit') AND comment <> '';
