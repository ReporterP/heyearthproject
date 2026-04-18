-- Irreversible data normalization. No-op on down migration: we cannot know
-- which rows originally had non-ISO city names vs which were legitimately LV/NL.
SELECT 1;
