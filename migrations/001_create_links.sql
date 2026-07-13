CREATE TABLE links (
                       id         SERIAL PRIMARY KEY,
                       short_code VARCHAR(10) NOT NULL UNIQUE,  -- короткий код (abc123)
                       long_url   TEXT NOT NULL,                 -- оригинальная длинная ссылка
                       clicks     INT NOT NULL DEFAULT 0,        -- счётчик переходов
                       created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);