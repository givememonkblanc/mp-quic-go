# MP-QUIC Jetson Depth+RGB Streaming System

## 문제 해결

Client가 bidirectional stream에서 ACK 응답을 성공적으로 수신했음에도 `EOF` 오류를 반환받는 문제가 발생했습니다.

### 원인
QUIC의 `Read()` 함수는 스트림의 write side가 닫힌 후에 `EOF`를 반환할 수 있으며, 이 경우에도 성공적으로 읽은 바이트 수(`n`)가 반환됩니다. client 코드는 단순히 `err != nil`로만 확인하여 실패로 처리했습니다.

### 해결 방법
```go
n, err := stream.Read(ack)
if err != nil && n != 2 {  // 2바이트 ACK가 왔는지 확인
    log.Printf("[%s] read ack: %v", name, err)
    stream.Close()
    return
}
```

## 동작 구성

### Server (`cmd/server/main.go`)
- `ListenEarly()` 사용 -> 0-RTT 지원
- MP-QUIC 세션 관리 (`internal/mpquic/session/`)
- Stream 라우팅 (`internal/handler/stream.go`)
  - depth stream (type=0x00)
  - RGB stream (type=0x01)

### Client (`cmd/jetson/main.go`)
- `DialEarly()` 사용 -> 세션 재사용 가능
- 두 개의 concurrent stream (depth + RGB)
- FPS 제어 (기본값 5fps)

### 테스트
```bash
# Server 시작
cd /home/ryzen395/mp-quic-go
./bin/server --config ./config/config.yaml

# Jetson Client 실행
sshpass -p 'REDACTED' ssh jetson@192.168.0.13 \
  "/home/jetson/jetson-client --addr 192.168.0.80:4433 --fps 5"
```

### 로그 예시
```
2026/06/20 19:24:31 Connected (path 0): [::]:47446 -> 192.168.0.80:4433
mp-quic session initialized: initial_path_id=0, selected_path_rssi=-34
```

## 핵심 수정파일
- `internal/handler/stream.go` - MultiStreamHandler
- `internal/conn/stream.go` - StreamHandler
- `cmd/jetson/main.go` - OpenStreamSync + Read logic fix
- `third_party/quic-go/server.go` - QUIC 서버 구현

## 멀티패스 2경로 전송 (동작 확인됨)

Jetson 클라이언트가 두 경로로 동시에 depth+RGB를 전송하고, 서버가 각 경로의
출발지 주소로 응답하는 양방향 2경로 전송이 실하드웨어에서 검증되었습니다.

- 클라이언트: `--path1 <addr2>` 로 두 번째 경로 추가 (`Connection.AddPath`),
  `PathSelector`(round-robin)로 패킷을 두 경로에 분배
- 서버: 패킷이 도착한 로컬 주소로 ACK/응답을 되돌려 보냄
  (경로별 `sendConn` + `IP_PKTINFO` 출발지 주소 고정)
- 구현 방식: **draft-21 정석** — per-path connection ID(§3.1/§4.4),
  per-path 패킷번호 공간 + path-ID AEAD nonce(§2.4), PATH_ACK(§4.1).
  경로는 connection ID로 식별되며 동일 4-tuple을 공유할 수 있음(§5.2).
  하드웨어에서 양방향 CID 교환 + path-1 업링크 데이터 + PATH_ACK 검증 완료.

테스트 예시 (동일 4-tuple, DCID로 경로 구분 — 별도 IP 불필요):
```bash
./bin/jetson --addr 192.168.0.80:4433 --path1 192.168.0.80:4433 --fps 5
# 서버 디버그 로그(QUIC_GO_LOG_LEVEL=debug)에서 PathAckFrame{PathID:1} 확인 가능
```

멀티패스 손실복구/혼잡제어: **경로별 독립** (§5.3/§5.4/§5.6/§5.7) — 경로마다
자체 RTT 추정·혼잡제어기·손실검출 타이머(run loop가 구동), `sendOnPath`는
경로별 혼잡 윈도우를 준수.

## 다음 단계
- 실제 Orbbec 카메라 통합
- qlogviewerDashboard 실시간 시각화
- 서버발(下) per-path 데이터 송신 시나리오 (현재 앱은 PATH_ACK만 反送)
- PATH_ABANDON / PATH_STATUS 전이의 transport 레벨 강제
