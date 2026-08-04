# Tiny URL Shortener

『요즘 개발자들을 위한 시스템 설계 수업』 9장을 참고한 URL 단축 서비스입니다.

## 요구사항

### MVP

- 긴 URL을 입력받아 고유한 고정 길이의 단축 키를 생성한다.
- 단축 키를 입력받아 원본 URL을 반환한다.
- 입력받은 URL이 유효한 HTTP 또는 HTTPS URL인지 검사한다.
- 동일한 긴 URL에는 동일한 단축 키를 반환한다.
- 존재하지 않는 단축 키와 잘못된 요청에 적절한 오류를 반환한다.
- 단축 URL 요청을 `307 Temporary Redirect`로 원본 URL에 리다이렉트한다.
- `user_id`를 기준으로 URL 생성자와 URL 소유권을 관리한다.

### 추후 지원

- 사용자가 원하는 맞춤형 단축 키를 생성한다.
- 마지막 사용 시점으로부터 6개월이 지난 단축 URL을 만료시킨다.
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

## 서비스 계층 계약

서비스 계층은 `Ab12Cd34`와 같은 `shortKey`만 반환한다. HTTP 계층은
서비스가 반환한 키에 기본 도메인을 붙여
`https://tiny.url/Ab12Cd34`와 같은 완성된 단축 URL을 생성한다.

서비스 계층에서 발생할 수 있는 오류는 다음과 같이 sentinel error로
정의한다.

```go
var (
	ErrInvalidUserID      = errors.New("invalid user id")
	ErrInvalidURL         = errors.New("invalid url")
	ErrURLMappingNotFound = errors.New("url mapping not found")
	ErrShortURLConflict   = errors.New("short URL already exists")
)
```

호출자는 `errors.Is`로 오류를 구분한다. HTTP 계층은 `ErrInvalidUserID`와
`ErrInvalidURL`을 `400 Bad Request`로, `ErrURLMappingNotFound`를
`404 Not Found`로 변환한다.

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

신규 매핑은 `201 Created`와 관리 리소스 경로를 반환한다.

```http
Location: /api/v1/short-urls/Ab12Cd34
```

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
