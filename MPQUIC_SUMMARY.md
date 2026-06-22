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

## 다음 단계
- 실제 Orbbec 카메라 통합
- qlogviewerDashboard 실시간 시각화
- multipath failover 테스트
