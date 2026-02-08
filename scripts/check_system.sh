#!/bin/bash

# Script to check system limits for rapid-proxy

echo "=== System Limits Check ==="
echo

# Check file descriptor limit
echo "File Descriptor Limit:"
ulimit -n
REQUIRED_FD=20000
CURRENT_FD=$(ulimit -n)

if [ "$CURRENT_FD" -lt "$REQUIRED_FD" ]; then
    echo "⚠️  WARNING: Current limit ($CURRENT_FD) is below recommended ($REQUIRED_FD)"
    echo "   Run: ulimit -n 65535"
    echo "   Or add to /etc/security/limits.conf:"
    echo "   * soft nofile 65535"
    echo "   * hard nofile 65535"
else
    echo "✅ OK: File descriptor limit is sufficient"
fi
echo

# Check TCP settings
echo "TCP Settings:"
echo -n "  net.core.rmem_max: "
sysctl -n net.core.rmem_max 2>/dev/null || echo "N/A"

echo -n "  net.core.wmem_max: "
sysctl -n net.core.wmem_max 2>/dev/null || echo "N/A"

echo -n "  net.ipv4.tcp_tw_reuse: "
sysctl -n net.ipv4.tcp_tw_reuse 2>/dev/null || echo "N/A"

echo -n "  net.ipv4.ip_local_port_range: "
sysctl -n net.ipv4.ip_local_port_range 2>/dev/null || echo "N/A"
echo

# Check available ports
CURRENT_RANGE=$(sysctl -n net.ipv4.ip_local_port_range 2>/dev/null)
if [ ! -z "$CURRENT_RANGE" ]; then
    START=$(echo $CURRENT_RANGE | awk '{print $1}')
    END=$(echo $CURRENT_RANGE | awk '{print $2}')
    AVAILABLE=$((END - START))

    if [ "$AVAILABLE" -lt 20000 ]; then
        echo "⚠️  WARNING: Limited port range ($AVAILABLE available)"
        echo "   Run: sudo sysctl -w net.ipv4.ip_local_port_range=\"10000 65535\""
    else
        echo "✅ OK: Port range is sufficient ($AVAILABLE available)"
    fi
fi
echo

# Check memory
echo "Memory:"
free -h
echo

# Recommendations
echo "=== Recommended Optimizations ==="
echo
echo "1. File Descriptors:"
echo "   ulimit -n 65535"
echo
echo "2. TCP Buffers:"
echo "   sudo sysctl -w net.core.rmem_max=16777216"
echo "   sudo sysctl -w net.core.wmem_max=16777216"
echo
echo "3. TCP Reuse:"
echo "   sudo sysctl -w net.ipv4.tcp_tw_reuse=1"
echo
echo "4. Port Range:"
echo "   sudo sysctl -w net.ipv4.ip_local_port_range=\"10000 65535\""
echo
echo "To make changes permanent, add to /etc/sysctl.conf"
