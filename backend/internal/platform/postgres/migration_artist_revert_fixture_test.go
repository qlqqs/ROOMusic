package postgres

func init() {
	// 往返测试的快照会比较 operations.created_at 的列默认值和既有行。
	rollbackFixtures[66] = `DO $fixture$
 BEGIN
   IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema()
       AND table_name='operations' AND column_name='created_at' AND column_default='clock_timestamp()') THEN
     RAISE EXCEPTION '0066 没有将操作时间默认值改为插入时间';
   END IF;
 END $fixture$;`
}
