#!/bin/bash
                           
                                                                                                       
                               

set -e

echo "=== SafeMath Lint: 检测裸 big.Int 运算 ==="

                                                                          
                                           
VIOLATIONS=$(grep -rn 'new(big\.Int)\.Add\|new(big\.Int)\.Sub\|new(big\.Int)\.Mul\|\.Add(.*big\.Int\|\.Sub(.*big\.Int\|\.Mul(.*big\.Int' \
  vm/ future/ \
  --include='*.go' \
  | grep -v 'safe_math\.go' \
  | grep -v '_test\.go' \
  | grep -v '// safe-math-ok' \
  || true)

if [ -n "$VIOLATIONS" ]; then
  echo ""
  echo "⚠️  发现裸 big.Int 运算（应使用 SafeAdd/SafeSub/SafeMul）："
  echo "$VIOLATIONS"
  echo ""
  echo "如果确实需要裸运算，请在行末添加 // safe-math-ok 注释"
  echo ""
  COUNT=$(echo "$VIOLATIONS" | wc -l)
  echo "共 $COUNT 处违规"
  exit 1
else
  echo "✅ 未发现裸 big.Int 运算，SafeMath 使用规范"
  exit 0
fi
