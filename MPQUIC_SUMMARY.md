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
- 구현 방식: **단일 공유 PN 공간** (경로는 네트워크 4-tuple로만 구분).
  포크에 path-ID AEAD nonce가 없어 per-path PN 공간 대신 단순화한 것으로,
  실용적 멀티패스에는 충분하나 draft-21 와이어 호환은 아님.

테스트 예시:
```bash
# 서버 호스트에 두 번째 도달 주소가 있을 때 (예: 보조 IP 또는 NIC)
./bin/jetson --addr 192.168.0.80:4433 --path1 192.168.0.81:4433 --fps 5
```

## 다음 단계
- 실제 Orbbec 카메라 통합
- qlogviewerDashboard 실시간 시각화
- draft-21 정식 멀티패스(per-path PN 공간 + path-ID AEAD nonce) 구현
- 경로별 독립 손실복구/혼잡제어
