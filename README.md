# System Design Playground

## 📌 Overview

『요즘 개발자들을 위한 시스템 설계 수업』의 요구사항을 바탕으로, 주요 분산 시스템의 핵심 패러다임을 직접 구현하고 최소한의 배포 환경까지 검증합니다.

- **`tiny_*` 모듈 구조**: 각 시스템 주제별 구현체는 레포지토리 최상위에 `tiny_<system_name>` 형태의 독립 디렉토리로 존재하며, 세부 명세 및 사용법은 각 디렉토리 내부 `README.md`에 기술합니다.
- **Go Idiomatic Adaptation**: 원본 요구사항의 도메인 흐름과 시스템 스펙은 충실히 준수하되, Goroutine, Channel, Context 등 Go 언어 고유의 동시성 모델과 관습에 맞게 재구성합니다.
- **Go Ecosystem First**: Key-Value Store, Message Queue, Cache 등 인프라 컴포넌트 구현 및 연동 시 Go 생태계의 패키지 및 클라이언트를 최우선으로 활용합니다.

---

## 🛠 Tech Stack & Environment

- **Language**: Go
- **Dev Environment**: Nix (`flake.nix`)
- **Git Hooks**: Lefthook

---

## 🚀 Getting Started

### 1. 개발 환경 세팅 (Nix)

```bash
# Nix Flake 개발 셸에 직접 진입
nix develop
```

direnv를 사용한다면 로컬 `.envrc`를 만들고 환경을 승인합니다. `.envrc`는 개인 개발 환경 설정이므로 Git에서 추적하지 않습니다.

```bash
echo 'use flake' > .envrc
direnv allow
```

### 2. Git Hooks 설치 (Lefthook)

개발 셸에서 설치 스크립트를 한 번 실행합니다.

```bash
./scripts/setup.sh
```

설치된 훅은 다음 검사를 수행합니다.

- **pre-commit**: 스테이징된 Go 파일 포맷 및 모든 `tiny_*` 모듈 테스트
- **pre-push**: 모든 `tiny_*` 모듈 린트

필요한 경우 훅을 직접 실행할 수 있습니다.

```bash
lefthook run pre-commit
lefthook run pre-push
```

---

## 📂 Directory Layout

```text
system-design-playground/
├── flake.nix                         
├── flake.lock                        
├── lefthook.yml                      
├── scripts/
│   ├── setup.sh                      # Lefthook 설치
│   └── for-each-tiny-project.sh      # 모든 tiny_* 모듈에서 명령 실행
└── tiny_<system>/                    # 시스템 설계 실습별 독립 Go 모듈
    ├── go.mod
    ├── README.md
    ├── cmd/
    └── internal/
```
