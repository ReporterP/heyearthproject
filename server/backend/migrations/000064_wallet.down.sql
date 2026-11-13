DROP TABLE IF EXISTS wallet_receipts;
DROP TABLE IF EXISTS wallet_transactions;
DROP TABLE IF EXISTS user_wallets;
DELETE FROM app_settings WHERE key = 'wallet_min_topup_rub';
