-- Подключение OAuth-клиента (коннектор Claude) к MCP-серверу pulse, разрешённое ключом доступа
-- (одним из MCP_EXTERNAL_TOKENS) на странице согласия. key_hash — sha256 этого ключа: ключ убран
-- из конфигурации — подключение не работает. Без connected_at — ждёт обмена кода на токены.
create table if not exists oauth_grant
(
    id             text        not null default gen_random_uuid()
        primary key,
    key_hash       text        not null,
    client_id      text        not null,
    client_name    text        not null default '',
    redirect_uri   text        not null default '',
    code_challenge text        not null default '',
    created_at     timestamptz not null default now(),
    connected_at   timestamptz,
    last_used_at   timestamptz
);

-- Коды авторизации, access- и refresh-токены подключений. Хранится только sha256 токена.
create table if not exists oauth_token
(
    token_hash text        not null
        primary key,
    grant_id   text        not null
        references oauth_grant (id) on delete cascade,
    kind       text        not null,
    expires_at timestamptz not null,
    created_at timestamptz not null default now()
);

create index if not exists oauth_token_grant_id_idx on oauth_token (grant_id);
create index if not exists oauth_token_expires_at_idx on oauth_token (expires_at);
