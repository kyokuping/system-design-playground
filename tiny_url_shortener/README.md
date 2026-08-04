# Tiny URL Shortener

『요즘 개발자들을 위한 시스템 설계 수업』 9장을 참고한 URL 단축 서비스입니다.

## 요구사항

### MVP

- 긴 URL을 입력받아 고유한 고정 길이의 단축 키를 생성한다.
- 단축 키는 Base62 문자 집합(`0-9`, `A-Z`, `a-z`)을 사용한다.
- 단축 키를 입력받아 원본 URL을 반환한다.
- 입력받은 URL이 유효한 HTTP 또는 HTTPS URL인지 검사한다.
- URL을 정규화한 후 동일한 URL인지 판단한다.
- 사용자와 관계없이 동일한 긴 URL을 요청하면 기존 단축 키를 공유한다.
- 존재하지 않는 단축 키와 잘못된 요청에 적절한 오류를 반환한다.
- 단축 URL 요청을 `307 Temporary Redirect`로 원본 URL에 리다이렉트한다.
- `user_id`를 기준으로 URL 생성자와 URL 소유권을 관리한다.

### 추후 지원

- 사용자가 원하는 맞춤형 단축 키를 생성한다.
- 마지막 사용 시점으로부터 6개월이 지난 단축 URL을 만료시키고,
  조회 시 `410 Gone`을 반환한다.
- URL 생성자가 원본 URL을 수정하거나 URL 매핑을 삭제한다.
- 단축 URL의 사용량을 분석하고 모니터링한다.

## 비기능적 요구사항

- 사용자 1억 명 규모와 갑작스러운 요청 증가에 대응할 수 있어야 한다.
- 읽기 요청이 쓰기 요청보다 많은 서비스를 가정한다.
- URL 조회 응답 시간은 p95 기준 100ms 이하를 목표로 한다.
- 동일한 단축 키에는 항상 동일한 원본 URL을 반환해야 한다.
- 시스템 장애가 발생해도 저장된 URL 매핑을 안전하게 유지해야 한다.

구체적인 일일 활성 사용자 수, URL 생성량, 초당 조회량은 용량 산정 단계에서
추가로 정의한다.

## Domain/Service 계층 계약

Domain과 Service 계층은 `Ab12Cd34`와 같은 `shortKey`만 반환한다.
Controller(API) 계층은 Service가 반환한 키에 기본 도메인을 붙여
`https://tiny.url/Ab12Cd34`와 같은 완성된 단축 URL을 생성한다.

Domain과 Service 계층에서 발생하는 오류는 도메인 전용 sentinel error로
정의한다. 예를 들어 URL 매핑을 찾을 수 없을 때는 `nil`이 아니라
`ErrURLMappingNotFound`를 반환한다.

```go
var (
	ErrInvalidUserID       = errors.New("invalid user id")
	ErrInvalidURL          = errors.New("invalid url")
	ErrURLMappingNotFound  = errors.New("url mapping not found")
	ErrURLMappingExpired   = errors.New("url mapping expired")
	ErrShortURLConflict    = errors.New("short URL already exists")
	ErrKeyGenerationFailed = errors.New("short key generation failed")
)
```

호출자는 `errors.Is`로 오류를 구분한다. Controller(API) 계층은 `ErrInvalidUserID`와
`ErrInvalidURL`을 `400 Bad Request`로, `ErrURLMappingNotFound`를
`404 Not Found`로, `ErrURLMappingExpired`를 `410 Gone`으로,
`ErrKeyGenerationFailed`를 `500 Internal Server Error`로 변환한다.

Base62 단축 키가 기존 키와 충돌하면 최초 시도 후 새 키 생성을
최대 3회 재시도한다. 3회의 재시도가 모두 실패하면 도메인 전용 오류인
`ErrKeyGenerationFailed`를 반환한다.

## URL 정규화

중복 URL을 조회하거나 새 매핑을 저장하기 전에 다음 규칙을 적용한다.

- 입력값의 앞뒤 공백을 제거한다.
- scheme과 host를 소문자로 변환한다.
- HTTP의 기본 포트 `80`과 HTTPS의 기본 포트 `443`을 제거한다.
- 빈 path를 `/`로 변환한다.
- path와 query parameter는 원래 값을 보존한다.

따라서 `HTTPS://EXAMPLE.COM:443`과 `https://example.com/`은 동일한 URL로
취급한다.

## API 계약

### URL 단축

`POST /api/v1/short-urls`

요청:

```json
{
  "user_id": "user-123",
  "url": "https://example.com/very/long/path"
}
```

신규 단축 키를 생성하면 `201 Created`와 관리 리소스 경로를 반환한다. 기존 URL
매핑을 재사용하면 동일한 응답 본문과 함께 `200 OK`를 반환한다.

```http
Location: /api/v1/short-urls/Ab12Cd34
```

응답 본문:

```json
{
  "short_key": "Ab12Cd34",
  "short_url": "https://tiny.url/Ab12Cd34",
  "long_url": "https://example.com/very/long/path"
}
```

잘못된 URL을 전달하면 `400 Bad Request`를 반환한다.

### 관리용 매핑 조회

`GET /api/v1/short-urls/{shortKey}`

성공하면 생성 응답과 같은 매핑 정보를 `200 OK` JSON으로 반환한다. 이 요청은
공개 링크 방문 횟수와 마지막 접근 시각을 갱신하지 않는다.

### 공개 리다이렉트

`GET /{shortKey}`

성공 응답 (`307 Temporary Redirect`):

```http
HTTP/1.1 307 Temporary Redirect
Location: https://example.com/very/long/path
```

단축 키가 존재하지 않으면 `404 Not Found`를 반환한다.
단축 URL이 만료되었다면 `410 Gone`을 반환한다.
