# Tasks: Login with email

**Input**: Design documents from `/specs/003-login/`

## Format: `[ID] [P?] [Story] Description`

## Phase 1: Setup (Shared Infrastructure)

- [x] T001 Create project structure per implementation plan
- [ ] T002 Initialize Go module with dependencies
- [ ]   T003   [P]   Configure linting in .golangci.yml

---

## Phase 2: Foundational (Blocking Prerequisites)

- [ ] T004 Setup database schema in internal/db/schema.go
- [ ] T005 [P] Implement session middleware in internal/auth/session.go
- [ ] T006 [P] Setup API routing in internal/api/router.go

**Checkpoint**: Foundation ready

## Phase 3: User Story 1 - Sign in (Priority: P1) 🎯 MVP

### Tests for User Story 1

- [ ] T007 [P] [US1] Contract test for POST /login in internal/auth/login_test.go
- [ ] T008 [P] [US1] Integration test for sign-in journey in tests/integration/test_login.py

### Implementation for User Story 1

- [ ] T009 [P] [US1] Create User model in internal/models/user.go
- [ ] T010 [US1] Implement LoginService in internal/auth/service.go (depends on T009)
- [X] T011 [US1] Add logging for sign-in

```bash
- [ ] T999 Example inside a fence is not a task
```

## Phase 4: Polish & Cross-Cutting Concerns

- [ ] T012 [P] Documentation updates in docs/
- [ ] T013 Run quickstart.md validation
