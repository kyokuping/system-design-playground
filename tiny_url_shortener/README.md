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
go run ./cmd/server
```

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

현재 서버는 배포 환경 검증을 위한 `GET /healthz` 엔드포인트만 제공합니다.
URL 단축 API와 저장소 연결은 이후 구현 단계에서 추가합니다.
