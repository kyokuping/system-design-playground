# Tiny URL Shortener

『요즘 개발자들을 위한 시스템 설계 수업』 9장을 참고한 URL 단축 서비스입니다.

## 문서

- [시스템 설계](docs/design.md)

## 실행 환경

개발 시 Go 애플리케이션은 Nix 개발 셸의 호스트 프로세스로 실행하고,
Postgres와 Redis만 Docker Compose로 실행합니다.

```bash
# 저장소 루트에서 실행
nix develop
cd tiny_url_shortener

docker compose \
  --env-file .env.dev \
  -f compose.yaml \
  -f compose.dev.yaml \
  up -d --wait

DATABASE_URL='postgres://app:dev@localhost:15432/app?sslmode=disable' \
REDIS_ADDR='localhost:16379' \
SHORT_URL_BASE='http://localhost:8080' \
go run ./cmd/server
```

`SERVER_ROLE`의 기본값은 로컬 개발용 `all`입니다. 운영과 같은 역할 분리 배포를
로컬에서 확인하려면 서로 다른 포트에서 command와 redirect 역할을 실행합니다.

```bash
SERVER_ROLE=command HTTP_ADDR=:8081 \
DATABASE_URL='postgres://app:dev@localhost:15432/app?sslmode=disable' \
REDIS_ADDR='localhost:16379' SHORT_URL_BASE='http://localhost:8082' \
go run ./cmd/server

SERVER_ROLE=redirect HTTP_ADDR=:8082 \
DATABASE_URL='postgres://app:dev@localhost:15432/app?sslmode=disable' \
REDIS_ADDR='localhost:16379' \
go run ./cmd/server
```

URL을 생성하고 조회하는 예시는 다음과 같습니다.

```bash
curl -i -X POST http://localhost:8080/api/v1/short-urls \
  -H 'Content-Type: application/json' \
  --data '{"user_id":"user-123","url":"https://example.com/long/path"}'

curl -i http://localhost:8080/api/v1/short-urls/0000000
curl -i http://localhost:8080/0000000
```

서버는 시작할 때 PostgreSQL 스키마를 준비하고, PostgreSQL을 URL 매핑과
분산 ID 범위의 원본 저장소로 사용합니다. Command 서버는 생성한 매핑을 PostgreSQL에
저장한 뒤 Redis 읽기 모델도 갱신합니다. Redirect 서버는 Redis cache-aside 조회를
사용하며, Redis에 연결할 수 없거나 cache miss이면 PostgreSQL로 조회합니다.
`ID_RANGE_SIZE`는 ID를 생성하는 Command 서버에만 적용됩니다(기본값 1000).

개발용 인프라의 로그 확인과 종료 명령은 다음과 같습니다.

```bash
docker compose --env-file .env.dev -f compose.yaml -f compose.dev.yaml logs --follow
docker compose --env-file .env.dev -f compose.yaml -f compose.dev.yaml down

go test ./...
docker build --tag tiny-url-shortener:dev .
```

개발용 Postgres는 `localhost:15432`, Redis는 `localhost:16379`에서
접근할 수 있습니다. 개발 데이터까지 초기화하려면 `down --volumes`를
사용한 뒤 인프라를 다시 시작합니다.

빌드한 이미지는 컨테이너 헬스체크를 포함합니다. `DATABASE_URL` 없이 실행하면
메모리 저장소로 기동하므로 Postgres와 Redis 없이 이미지만 확인할 수 있습니다.

```bash
docker run --rm -d --name tiny-url-shortener-dev -p 8080:8080 \
  -e SERVER_ROLE=all -e SHORT_URL_BASE=http://localhost:8080 tiny-url-shortener:dev
docker ps --filter name=tiny-url-shortener-dev --format '{{.Status}}'
docker stop tiny-url-shortener-dev
```

서버는 `POST /api/v1/short-urls`, `GET /api/v1/short-urls/{shortKey}`,
`GET /{shortKey}`, `GET /healthz`를 제공합니다. API 계약과 오류 응답은
[시스템 설계](docs/design.md)에 기록되어 있습니다.
