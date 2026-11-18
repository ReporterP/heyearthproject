-- A hedgehog scene as a news post's picture (spec 12 §12.6): a slug from cmd/api/tgscenes.go;
-- never together with image_path (the API checks).
ALTER TABLE news_posts ADD COLUMN IF NOT EXISTS scene TEXT;
