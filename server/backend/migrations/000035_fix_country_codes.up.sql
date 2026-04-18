-- Clean up legacy rows where country_code stored a city/location name instead
-- of an ISO-3166 alpha-2 code. From now on AddServerHandler / UpdateServerHandler
-- validate and uppercase the value.
--
-- Known cases observed in production (admin QA 2026-04-18):
--   * "Амстердам" / "Amsterdam"   → NL
--   * "Латвия"    / "Latvia"      → LV
UPDATE servers
SET country_code = 'NL'
WHERE LOWER(country_code) IN ('амстердам', 'amsterdam');

UPDATE servers
SET country_code = 'LV'
WHERE LOWER(country_code) IN ('латвия', 'latvia');

-- Generic safety net: any remaining country_code that is NOT exactly two
-- ASCII letters gets blanked so it doesn't trip the new validator on next
-- write.
UPDATE servers
SET country_code = ''
WHERE country_code IS NOT NULL
  AND country_code !~ '^[A-Za-z]{2}$'
  AND TRIM(country_code) <> '';

-- Uppercase any valid 2-letter codes that slipped in lowercase.
UPDATE servers
SET country_code = UPPER(country_code)
WHERE country_code ~ '^[a-z]{2}$';
