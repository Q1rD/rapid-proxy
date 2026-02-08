#!/bin/bash

# Script to start mock proxy farm for testing

MOCK_PROXY_SCRIPT="$HOME/tools/mock-proxy-farm/mock_proxy_farm.py"
PID_FILE="/tmp/rapid-proxy-mock-farm.pid"

case "$1" in
    start)
        echo "Starting mock proxy farm (1000 proxies on ports 10000-10999)..."

        if [ -f "$PID_FILE" ]; then
            echo "⚠️  Mock proxy farm is already running (PID: $(cat $PID_FILE))"
            exit 1
        fi

        if [ ! -f "$MOCK_PROXY_SCRIPT" ]; then
            echo "❌ Mock proxy script not found at: $MOCK_PROXY_SCRIPT"
            exit 1
        fi

        # Start in background
        python3 "$MOCK_PROXY_SCRIPT" > /tmp/rapid-proxy-mock-farm.log 2>&1 &
        echo $! > "$PID_FILE"

        # Wait a bit for startup
        sleep 2

        if ps -p $(cat $PID_FILE) > /dev/null 2>&1; then
            echo "✅ Mock proxy farm started successfully"
            echo "   PID: $(cat $PID_FILE)"
            echo "   Logs: /tmp/rapid-proxy-mock-farm.log"
            echo "   Proxies available at: http://127.0.0.1:10000 - http://127.0.0.1:10999"
        else
            echo "❌ Failed to start mock proxy farm"
            cat /tmp/rapid-proxy-mock-farm.log
            rm -f "$PID_FILE"
            exit 1
        fi
        ;;

    stop)
        echo "Stopping mock proxy farm..."

        if [ ! -f "$PID_FILE" ]; then
            echo "⚠️  Mock proxy farm is not running"
            exit 1
        fi

        PID=$(cat $PID_FILE)
        if ps -p $PID > /dev/null 2>&1; then
            kill $PID
            sleep 1

            if ps -p $PID > /dev/null 2>&1; then
                echo "Force killing..."
                kill -9 $PID
            fi

            echo "✅ Mock proxy farm stopped"
        else
            echo "⚠️  Process not found (PID: $PID)"
        fi

        rm -f "$PID_FILE"
        ;;

    status)
        if [ -f "$PID_FILE" ]; then
            PID=$(cat $PID_FILE)
            if ps -p $PID > /dev/null 2>&1; then
                echo "✅ Mock proxy farm is running (PID: $PID)"

                # Test a proxy
                if curl -s --max-time 2 http://127.0.0.1:10000 > /dev/null 2>&1; then
                    echo "   Proxy test: OK"
                else
                    echo "   ⚠️  Proxy test: FAILED"
                fi
            else
                echo "❌ Mock proxy farm is not running (stale PID file)"
                rm -f "$PID_FILE"
            fi
        else
            echo "❌ Mock proxy farm is not running"
        fi
        ;;

    restart)
        $0 stop
        sleep 1
        $0 start
        ;;

    *)
        echo "Usage: $0 {start|stop|status|restart}"
        exit 1
        ;;
esac
