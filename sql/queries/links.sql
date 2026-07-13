-- name: CreateLink :one
INSERT INTO links (
    short_code,
    long_url
)
VALUES (
           $1,
           $2
       )
    RETURNING
    id,
    short_code,
    long_url,
    clicks,
    created_at;

-- name: GetLinkByCode :one
SELECT
    id,
    short_code,
    long_url,
    clicks,
    created_at
FROM links
WHERE short_code = $1;

-- name: IncrementClicks :execrows
UPDATE links
SET clicks = clicks + 1
WHERE short_code = $1;