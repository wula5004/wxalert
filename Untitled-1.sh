curl -X POST "http://10.0.0.200:18080/webhook" \
  -H "Content-Type: application/json" \
  -d '{"title":"测试","content":"测试接口，code=500","template":"markdown"}'



curl -X POST "http://10.0.0.200:18080/webhook" \
  -H "Content-Type: application/json" \
  -d '{
   "title":"零件检测结果",
    "尺寸是否合格": "aaaaa",
    "具体数据": {"尺寸": "100", "单位": "mm"},
    "不合格尺寸": "bbbbbb"
  }'

curl -X POST http://10.0.0.200:18080/webhook \
  -H 'Content-Type: application/json' \
  -d '{
    "machine_id": "node1",
    "title": "零件告警",
    "level": "CRITICAL",
    "service": "web-api",
    "msg": "xxxxxxxxxxxxxxxxx"
  }'