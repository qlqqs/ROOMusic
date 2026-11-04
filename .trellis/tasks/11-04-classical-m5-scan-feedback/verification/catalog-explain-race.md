# 最近新增与加入时间排序：规模验证

## 环境与方法

- 一次性 Compose 项目 roomusic-test-m5f，随机测试数据库、独立 schema；没有访问用户数据。
- 合成 8,000 组、10,000 发行、100,000 曲目，5,000 曲目 missing（5%）；前2,000组双版本，其余单版本，其中250发行整版缺失、2,500发行各缺失1轨，共7,800个可见组。
- 核心四表仅在测试准备时 ANALYZE。五个空元数据辅助表禁止自动统计，先验证未 ANALYZE（reltuples=-1），再显式 ANALYZE（reltuples=0）。业务请求不执行 ANALYZE、不修改任何 enable_* 或全局/会话配置。
- 每个只读 REPEATABLE READ 事务开始后 SET LOCAL jit = off；同一物理连接事务内 SHOW jit=off，结束后与连接原默认值相同。
- 下文捕获真实 Store 查询和参数，含分页COUNT，运行 EXPLAIN (ANALYZE, BUFFERS)。完整 HTTP 服务端 p95 见本文件的 HTTP 结果小节和 catalog-http-performance.md；SQL执行时间不能代替HTTP门槛。
- PostgreSQL：18.6。

## 辅助空表未 ANALYZE

- `release_corrections`: reltuples=-1。
- `release_field_decisions`: reltuples=-1。
- `release_grouping_evidence`: reltuples=-1。
- `release_issue_reviews`: reltuples=-1。
- `release_artworks`: reltuples=-1。

### 最近8项（无COUNT）

参数：`[]`；事务jit=off。

```text
Limit  (cost=256490.20..259027.94 rows=8 width=364) (actual time=118.739..120.341 rows=8.00 loops=1)
  Buffers: shared hit=32742
  ->  Result  (cost=256490.20..272351.06 rows=50 width=364) (actual time=118.737..120.337 rows=8.00 loops=1)
        Buffers: shared hit=32742
        ->  Sort  (cost=256490.20..256490.32 rows=50 width=348) (actual time=118.258..118.280 rows=8.00 loops=1)
              Sort Key: ranked.added_at DESC, ranked.group_id DESC
              Sort Method: top-N heapsort  Memory: 28kB
              Buffers: shared hit=32099
              ->  Nested Loop Left Join  (cost=89399.26..256489.20 rows=50 width=348) (actual time=96.017..115.668 rows=7800.00 loops=1)
                    Join Filter: (release_artwork.release_id = r.id)
                    Buffers: shared hit=32099
                    ->  Hash Join  (cost=89399.26..256149.95 rows=50 width=262) (actual time=95.989..111.762 rows=7800.00 loops=1)
                          Hash Cond: (ranked.id = r.id)
                          Buffers: shared hit=32099
                          ->  Subquery Scan on ranked  (cost=88837.12..255587.12 rows=50 width=48) (actual time=87.398..100.476 rows=7800.00 loops=1)
                                Filter: (ranked.member_rank = 1)
                                Buffers: shared hit=31883
                                ->  WindowAgg  (cost=88837.12..255462.12 rows=10000 width=70) (actual time=87.396..99.670 rows=7800.00 loops=1)
                                      Window: w2 AS (PARTITION BY r_1.group_id)
                                      Filter: ((row_number() OVER w1) <= 1)
                                      Rows Removed by Filter: 1950
                                      Storage: Memory  Maximum Storage: 17kB
                                      Buffers: shared hit=31883
                                      ->  WindowAgg  (cost=88837.12..172212.12 rows=10000 width=54) (actual time=87.346..92.599 rows=9750.00 loops=1)
                                            Window: w1 AS (PARTITION BY r_1.group_id ORDER BY ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id ROWS UNBOUNDED PRECEDING)
                                            Run Condition: (row_number() OVER w1 <= 1)
                                            Storage: Memory  Maximum Storage: 17kB
                                            Buffers: shared hit=31883
                                            ->  Sort  (cost=88837.12..88862.12 rows=10000 width=46) (actual time=87.330..88.260 rows=9750.00 loops=1)
                                                  Sort Key: r_1.group_id, ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id
                                                  Sort Method: quicksort  Memory: 994kB
                                                  Buffers: shared hit=31883
                                                  ->  Hash Join  (cost=4620.98..88172.74 rows=10000 width=46) (actual time=61.592..83.842 rows=9750.00 loops=1)
                                                        Hash Cond: (r_1.id = visible_media.release_id)
                                                        Buffers: shared hit=31883
                                                        ->  Hash Left Join  (cost=15.12..354.38 rows=10000 width=64) (actual time=0.059..4.048 rows=10000.00 loops=1)
                                                              Hash Cond: (r_1.id = c_year_1.release_id)
                                                              Buffers: shared hit=214
                                                              ->  Seq Scan on releases r_1  (cost=0.00..313.00 rows=10000 width=44) (actual time=0.018..2.579 rows=10000.00 loops=1)
                                                                    Buffers: shared hit=213
                                                              ->  Hash  (cost=15.08..15.08 rows=3 width=20) (actual time=0.023..0.025 rows=0.00 loops=1)
                                                                    Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                                    Buffers: shared hit=1
                                                                    ->  Bitmap Heap Scan on release_corrections c_year_1  (cost=7.97..15.08 rows=3 width=20) (actual time=0.023..0.023 rows=0.00 loops=1)
                                                                          Recheck Cond: (field = 'year'::text)
                                                                          Buffers: shared hit=1
                                                                          ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.018..0.018 rows=0.00 loops=1)
                                                                                Index Cond: (field = 'year'::text)
                                                                                Index Searches: 1
                                                                                Buffers: shared hit=1
                                                        ->  Hash  (cost=4480.86..4480.86 rows=10000 width=16) (actual time=61.411..61.415 rows=9750.00 loops=1)
                                                              Buckets: 16384  Batches: 1  Memory Usage: 586kB
                                                              Buffers: shared hit=2419
                                                              ->  HashAggregate  (cost=4380.86..4480.86 rows=10000 width=16) (actual time=58.938..59.803 rows=9750.00 loops=1)
                                                                    Group Key: visible_media.release_id
                                                                    Batches: 1  Memory Usage: 793kB
                                                                    Buffers: shared hit=2419
                                                                    ->  Hash Join  (cost=319.00..4143.41 rows=94977 width=16) (actual time=2.661..42.937 rows=95000.00 loops=1)
                                                                          Hash Cond: (visible_tracks.medium_id = visible_media.id)
                                                                          Buffers: shared hit=2419
                                                                          ->  Seq Scan on tracks visible_tracks  (cost=0.00..3575.00 rows=94977 width=16) (actual time=0.014..22.279 rows=95000.00 loops=1)
                                                                                Filter: (source_status = 'present'::text)
                                                                                Rows Removed by Filter: 5000
                                                                                Buffers: shared hit=2325
                                                                          ->  Hash  (cost=194.00..194.00 rows=10000 width=32) (actual time=2.569..2.571 rows=10000.00 loops=1)
                                                                                Buckets: 16384  Batches: 1  Memory Usage: 753kB
                                                                                Buffers: shared hit=94
                                                                                ->  Seq Scan on media visible_media  (cost=0.00..194.00 rows=10000 width=32) (actual time=0.007..0.899 rows=10000.00 loops=1)
                                                                                      Buffers: shared hit=94
                                                        SubPlan 12
                                                          ->  Index Scan using release_groups_pkey on release_groups default_group  (cost=0.28..8.30 rows=1 width=16) (actual time=0.001..0.001 rows=1.00 loops=9750)
                                                                Index Cond: (id = r_1.group_id)
                                                                Index Searches: 9750
                                                                Buffers: shared hit=29250
                          ->  Hash  (cost=437.14..437.14 rows=10000 width=230) (actual time=8.513..8.521 rows=10000.00 loops=1)
                                Buckets: 16384  Batches: 1  Memory Usage: 1027kB
                                Buffers: shared hit=216
                                ->  Hash Left Join  (cost=45.36..437.14 rows=10000 width=230) (actual time=0.110..6.040 rows=10000.00 loops=1)
                                      Hash Cond: (r.id = c_year.release_id)
                                      Buffers: shared hit=216
                                      ->  Hash Left Join  (cost=30.24..395.76 rows=10000 width=210) (actual time=0.064..4.525 rows=10000.00 loops=1)
                                            Hash Cond: (r.id = c_album_artist.release_id)
                                            Buffers: shared hit=215
                                            ->  Hash Left Join  (cost=15.12..354.38 rows=10000 width=178) (actual time=0.043..3.180 rows=10000.00 loops=1)
                                                  Hash Cond: (r.id = c_title.release_id)
                                                  Buffers: shared hit=214
                                                  ->  Seq Scan on releases r  (cost=0.00..313.00 rows=10000 width=146) (actual time=0.012..1.738 rows=10000.00 loops=1)
                                                        Buffers: shared hit=213
                                                  ->  Hash  (cost=15.08..15.08 rows=3 width=48) (actual time=0.010..0.011 rows=0.00 loops=1)
                                                        Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                        Buffers: shared hit=1
                                                        ->  Bitmap Heap Scan on release_corrections c_title  (cost=7.97..15.08 rows=3 width=48) (actual time=0.009..0.010 rows=0.00 loops=1)
                                                              Recheck Cond: (field = 'title'::text)
                                                              Buffers: shared hit=1
                                                              ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.005..0.005 rows=0.00 loops=1)
                                                                    Index Cond: (field = 'title'::text)
                                                                    Index Searches: 1
                                                                    Buffers: shared hit=1
                                            ->  Hash  (cost=15.08..15.08 rows=3 width=48) (actual time=0.004..0.006 rows=0.00 loops=1)
                                                  Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                  Buffers: shared hit=1
                                                  ->  Bitmap Heap Scan on release_corrections c_album_artist  (cost=7.97..15.08 rows=3 width=48) (actual time=0.004..0.005 rows=0.00 loops=1)
                                                        Recheck Cond: (field = 'album_artist'::text)
                                                        Buffers: shared hit=1
                                                        ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.001..0.002 rows=0.00 loops=1)
                                                              Index Cond: (field = 'album_artist'::text)
                                                              Index Searches: 1
                                                              Buffers: shared hit=1
                                      ->  Hash  (cost=15.08..15.08 rows=3 width=20) (actual time=0.011..0.012 rows=0.00 loops=1)
                                            Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                            Buffers: shared hit=1
                                            ->  Bitmap Heap Scan on release_corrections c_year  (cost=7.97..15.08 rows=3 width=20) (actual time=0.011..0.011 rows=0.00 loops=1)
                                                  Recheck Cond: (field = 'year'::text)
                                                  Buffers: shared hit=1
                                                  ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.002..0.002 rows=0.00 loops=1)
                                                        Index Cond: (field = 'year'::text)
                                                        Index Searches: 1
                                                        Buffers: shared hit=1
                    ->  Materialize  (cost=0.00..16.45 rows=430 width=88) (actual time=0.000..0.000 rows=0.00 loops=7800)
                          Storage: Memory  Maximum Storage: 17kB
                          ->  Seq Scan on release_artworks release_artwork  (cost=0.00..14.30 rows=430 width=88) (actual time=0.014..0.014 rows=0.00 loops=1)
        SubPlan 1
          ->  Aggregate  (cost=51.27..51.28 rows=1 width=8) (actual time=0.072..0.072 rows=1.00 loops=8)
                Buffers: shared hit=128
                ->  Sort  (cost=51.22..51.25 rows=9 width=16) (actual time=0.060..0.061 rows=9.75 loops=8)
                      Sort Key: present_medium.id
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=128
                      ->  Nested Loop  (cost=4.78..51.08 rows=9 width=16) (actual time=0.036..0.044 rows=9.75 loops=8)
                            Buffers: shared hit=128
                            ->  Index Scan using media_release_position_uq on media present_medium  (cost=0.29..8.30 rows=1 width=16) (actual time=0.007..0.008 rows=1.00 loops=8)
                                  Index Cond: (release_id = r.id)
                                  Index Searches: 8
                                  Buffers: shared hit=24
                            ->  Bitmap Heap Scan on tracks present_track  (cost=4.50..42.68 rows=10 width=16) (actual time=0.021..0.027 rows=9.75 loops=8)
                                  Recheck Cond: (present_medium.id = medium_id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Heap Blocks: exact=80
                                  Buffers: shared hit=104
                                  ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.015..0.015 rows=10.00 loops=8)
                                        Index Cond: (medium_id = present_medium.id)
                                        Index Searches: 8
                                        Buffers: shared hit=24
        SubPlan 2
          ->  Aggregate  (cost=51.10..51.11 rows=1 width=8) (actual time=0.037..0.037 rows=1.00 loops=8)
                Buffers: shared hit=128
                ->  Nested Loop  (cost=4.78..51.08 rows=9 width=0) (actual time=0.029..0.035 rows=9.75 loops=8)
                      Buffers: shared hit=128
                      ->  Index Scan using media_release_position_uq on media present_medium_1  (cost=0.29..8.30 rows=1 width=16) (actual time=0.003..0.003 rows=1.00 loops=8)
                            Index Cond: (release_id = r.id)
                            Index Searches: 8
                            Buffers: shared hit=24
                      ->  Bitmap Heap Scan on tracks present_track_1  (cost=4.50..42.68 rows=10 width=16) (actual time=0.017..0.022 rows=9.75 loops=8)
                            Recheck Cond: (present_medium_1.id = medium_id)
                            Filter: (source_status = 'present'::text)
                            Rows Removed by Filter: 0
                            Heap Blocks: exact=80
                            Buffers: shared hit=104
                            ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.014..0.014 rows=10.00 loops=8)
                                  Index Cond: (medium_id = present_medium_1.id)
                                  Index Searches: 8
                                  Buffers: shared hit=24
        SubPlan 3
          ->  Aggregate  (cost=24.53..24.54 rows=1 width=8) (actual time=0.004..0.004 rows=1.00 loops=8)
                Buffers: shared hit=16
                ->  Nested Loop Anti Join  (cost=0.44..24.53 rows=1 width=32) (actual time=0.003..0.003 rows=0.00 loops=8)
                      Buffers: shared hit=16
                      ->  Nested Loop Anti Join  (cost=0.29..16.35 rows=1 width=144) (actual time=0.003..0.003 rows=0.00 loops=8)
                            Buffers: shared hit=16
                            ->  Index Scan using release_field_decisions_release_id_field_key_key on release_field_decisions decision  (cost=0.14..8.16 rows=1 width=144) (actual time=0.002..0.002 rows=0.00 loops=8)
                                  Index Cond: (release_id = r.id)
                                  Filter: (action = 'uncertain_apply'::text)
                                  Index Searches: 8
                                  Buffers: shared hit=16
                            ->  Index Only Scan using release_corrections_pkey on release_corrections correction  (cost=0.15..8.17 rows=1 width=48) (never executed)
                                  Index Cond: ((release_id = r.id) AND (field = CASE decision.field_key WHEN 'artist'::text THEN 'album_artist'::text ELSE decision.field_key END))
                                  Heap Fetches: 0
                                  Index Searches: 0
                      ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review  (cost=0.15..8.18 rows=1 width=80) (never executed)
                            Index Cond: ((release_id = r.id) AND (issue = decision.field_key))
                            Filter: (basis = jsonb_build_object('reason', decision.reason, 'value', decision.selected_value, 'candidates', decision.candidates))
                            Index Searches: 0
        SubPlan 5
          ->  Hash Anti Join  (cost=16.18..37.69 rows=378 width=16) (actual time=0.006..0.007 rows=0.00 loops=1)
                Hash Cond: (("grouping".release_id = review_1.release_id) AND (jsonb_build_object('reason', "grouping".reason, 'source_refs', "grouping".source_refs) = review_1.basis))
                ->  Seq Scan on release_grouping_evidence "grouping"  (cost=0.00..15.70 rows=378 width=80) (actual time=0.006..0.006 rows=0.00 loops=1)
                      Filter: (NULLIF(btrim(reason), ''::text) IS NOT NULL)
                ->  Hash  (cost=16.13..16.13 rows=3 width=48) (never executed)
                      ->  Bitmap Heap Scan on release_issue_reviews review_1  (cost=9.03..16.13 rows=3 width=48) (never executed)
                            Recheck Cond: (issue = 'grouping'::text)
                            ->  Bitmap Index Scan on release_issue_reviews_pkey  (cost=0.00..9.03 rows=3 width=0) (never executed)
                                  Index Cond: (issue = 'grouping'::text)
                                  Index Searches: 0
        SubPlan 6
          ->  Aggregate  (cost=59.22..59.23 rows=1 width=8) (actual time=0.055..0.055 rows=1.00 loops=8)
                Buffers: shared hit=132
                ->  Sort  (cost=59.20..59.21 rows=1 width=69) (actual time=0.046..0.046 rows=0.25 loops=8)
                      Sort Key: (COALESCE(NULLIF(missing_track.source_identity, ''::text), (((missing_track.source_root_id)::text || ':'::text) || missing_track.relative_path)))
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=132
                      ->  Nested Loop Anti Join  (cost=4.93..59.19 rows=1 width=69) (actual time=0.032..0.034 rows=0.25 loops=8)
                            Join Filter: (((review_2.basis -> 'tracks'::text) -> (missing_track.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track.observed_at) * '1000000'::numeric))::bigint))
                            Buffers: shared hit=132
                            ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.029..0.030 rows=0.25 loops=8)
                                  Buffers: shared hit=128
                                  ->  Index Scan using media_release_position_uq on media missing_medium  (cost=0.29..8.30 rows=1 width=32) (actual time=0.003..0.004 rows=1.00 loops=8)
                                        Index Cond: (release_id = r.id)
                                        Index Searches: 8
                                        Buffers: shared hit=24
                                  ->  Bitmap Heap Scan on tracks missing_track  (cost=4.49..42.68 rows=1 width=109) (actual time=0.018..0.019 rows=0.25 loops=8)
                                        Recheck Cond: (missing_medium.id = medium_id)
                                        Filter: (source_status = 'missing'::text)
                                        Rows Removed by Filter: 10
                                        Heap Blocks: exact=80
                                        Buffers: shared hit=104
                                        ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.011..0.011 rows=10.00 loops=8)
                                              Index Cond: (medium_id = missing_medium.id)
                                              Index Searches: 8
                                              Buffers: shared hit=24
                            ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review_2  (cost=0.15..8.17 rows=1 width=48) (actual time=0.007..0.007 rows=0.00 loops=2)
                                  Index Cond: ((release_id = r.id) AND (issue = 'missing_tracks'::text))
                                  Index Searches: 2
                                  Buffers: shared hit=4
        SubPlan 11
          ->  Aggregate  (cost=114.64..114.65 rows=1 width=8) (actual time=0.078..0.079 rows=1.00 loops=8)
                Buffers: shared hit=239
                ->  Nested Loop Semi Join  (cost=0.99..14.50 rows=1 width=16) (actual time=0.013..0.015 rows=1.00 loops=8)
                      Buffers: shared hit=75
                      ->  Index Only Scan using releases_group_created_id_index on releases r_2  (cost=0.29..4.30 rows=1 width=16) (actual time=0.006..0.006 rows=1.00 loops=8)
                            Index Cond: (group_id = ranked.group_id)
                            Heap Fetches: 0
                            Index Searches: 8
                            Buffers: shared hit=17
                      ->  Nested Loop  (cost=0.70..10.18 rows=9 width=16) (actual time=0.007..0.007 rows=1.00 loops=8)
                            Buffers: shared hit=58
                            ->  Index Scan using media_release_position_uq on media visible_media_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.002..0.002 rows=1.00 loops=8)
                                  Index Cond: (release_id = r_2.id)
                                  Index Searches: 8
                                  Buffers: shared hit=24
                            ->  Index Scan using tracks_release_index on tracks visible_tracks_1  (cost=0.42..1.78 rows=10 width=16) (actual time=0.003..0.003 rows=1.00 loops=8)
                                  Index Cond: (medium_id = visible_media_1.id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Index Searches: 8
                                  Buffers: shared hit=34
                SubPlan 7
                  ->  Aggregate  (cost=24.53..24.54 rows=1 width=8) (actual time=0.003..0.003 rows=1.00 loops=8)
                        Buffers: shared hit=16
                        ->  Nested Loop Anti Join  (cost=0.44..24.53 rows=1 width=32) (actual time=0.003..0.003 rows=0.00 loops=8)
                              Buffers: shared hit=16
                              ->  Nested Loop Anti Join  (cost=0.29..16.35 rows=1 width=144) (actual time=0.002..0.002 rows=0.00 loops=8)
                                    Buffers: shared hit=16
                                    ->  Index Scan using release_field_decisions_release_id_field_key_key on release_field_decisions decision_1  (cost=0.14..8.16 rows=1 width=144) (actual time=0.002..0.002 rows=0.00 loops=8)
                                          Index Cond: (release_id = r_2.id)
                                          Filter: (action = 'uncertain_apply'::text)
                                          Index Searches: 8
                                          Buffers: shared hit=16
                                    ->  Index Only Scan using release_corrections_pkey on release_corrections correction_1  (cost=0.15..8.17 rows=1 width=48) (never executed)
                                          Index Cond: ((release_id = r_2.id) AND (field = CASE decision_1.field_key WHEN 'artist'::text THEN 'album_artist'::text ELSE decision_1.field_key END))
                                          Heap Fetches: 0
                                          Index Searches: 0
                              ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review_3  (cost=0.15..8.18 rows=1 width=80) (never executed)
                                    Index Cond: ((release_id = r_2.id) AND (issue = decision_1.field_key))
                                    Filter: (basis = jsonb_build_object('reason', decision_1.reason, 'value', decision_1.selected_value, 'candidates', decision_1.candidates))
                                    Index Searches: 0
                SubPlan 8
                  ->  Nested Loop Anti Join  (cost=0.30..16.36 rows=1 width=0) (actual time=0.002..0.002 rows=0.00 loops=8)
                        Join Filter: (review_4.basis = jsonb_build_object('reason', grouping_1.reason, 'source_refs', grouping_1.source_refs))
                        Buffers: shared hit=16
                        ->  Index Scan using release_grouping_evidence_current_uq on release_grouping_evidence grouping_1  (cost=0.15..8.17 rows=1 width=80) (actual time=0.002..0.002 rows=0.00 loops=8)
                              Index Cond: (release_id = r_2.id)
                              Filter: (NULLIF(btrim(reason), ''::text) IS NOT NULL)
                              Index Searches: 8
                              Buffers: shared hit=16
                        ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review_4  (cost=0.15..8.17 rows=1 width=48) (never executed)
                              Index Cond: ((release_id = r_2.id) AND (issue = 'grouping'::text))
                              Index Searches: 0
                SubPlan 10
                  ->  Aggregate  (cost=59.22..59.23 rows=1 width=8) (actual time=0.054..0.054 rows=1.00 loops=8)
                        Buffers: shared hit=132
                        ->  Sort  (cost=59.20..59.21 rows=1 width=69) (actual time=0.043..0.043 rows=0.25 loops=8)
                              Sort Key: (COALESCE(NULLIF(missing_track_1.source_identity, ''::text), (((missing_track_1.source_root_id)::text || ':'::text) || missing_track_1.relative_path)))
                              Sort Method: quicksort  Memory: 25kB
                              Buffers: shared hit=132
                              ->  Nested Loop Anti Join  (cost=4.93..59.19 rows=1 width=69) (actual time=0.031..0.033 rows=0.25 loops=8)
                                    Join Filter: (((review_5.basis -> 'tracks'::text) -> (missing_track_1.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track_1.observed_at) * '1000000'::numeric))::bigint))
                                    Buffers: shared hit=132
                                    ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.029..0.030 rows=0.25 loops=8)
                                          Buffers: shared hit=128
                                          ->  Index Scan using media_release_position_uq on media missing_medium_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.003..0.004 rows=1.00 loops=8)
                                                Index Cond: (release_id = r_2.id)
                                                Index Searches: 8
                                                Buffers: shared hit=24
                                          ->  Bitmap Heap Scan on tracks missing_track_1  (cost=4.49..42.68 rows=1 width=109) (actual time=0.018..0.019 rows=0.25 loops=8)
                                                Recheck Cond: (missing_medium_1.id = medium_id)
                                                Filter: (source_status = 'missing'::text)
                                                Rows Removed by Filter: 10
                                                Heap Blocks: exact=80
                                                Buffers: shared hit=104
                                                ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.011..0.011 rows=10.00 loops=8)
                                                      Index Cond: (medium_id = missing_medium_1.id)
                                                      Index Searches: 8
                                                      Buffers: shared hit=24
                                    ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review_5  (cost=0.15..8.17 rows=1 width=48) (actual time=0.006..0.006 rows=0.00 loops=2)
                                          Index Cond: ((release_id = r_2.id) AND (issue = 'missing_tracks'::text))
                                          Index Searches: 2
                                          Buffers: shared hit=4
Planning:
  Buffers: shared hit=180
Planning Time: 5.164 ms
Execution Time: 122.259 ms
```

### 加入时间排序首页50项（含COUNT）

参数：`[]`；事务jit=off。

```text
Aggregate  (cost=172337.25..172337.26 rows=1 width=8) (actual time=92.710..92.719 rows=1.00 loops=1)
  Buffers: shared hit=31897
  ->  Subquery Scan on ranked  (cost=88837.12..172337.12 rows=50 width=0) (actual time=87.024..92.315 rows=7800.00 loops=1)
        Filter: (ranked.member_rank = 1)
        Buffers: shared hit=31897
        ->  WindowAgg  (cost=88837.12..172212.12 rows=10000 width=70) (actual time=87.024..91.615 rows=7800.00 loops=1)
              Window: w1 AS (PARTITION BY r.group_id ORDER BY ((r.id IS DISTINCT FROM (SubPlan 1))), ((NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0)), r.created_at, r.id ROWS UNBOUNDED PRECEDING)
              Run Condition: (row_number() OVER w1 <= 1)
              Storage: Memory  Maximum Storage: 17kB
              Buffers: shared hit=31897
              ->  Sort  (cost=88837.12..88862.12 rows=10000 width=46) (actual time=87.013..87.959 rows=9750.00 loops=1)
                    Sort Key: r.group_id, ((r.id IS DISTINCT FROM (SubPlan 1))), ((NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0)), r.created_at, r.id
                    Sort Method: quicksort  Memory: 994kB
                    Buffers: shared hit=31897
                    ->  Hash Join  (cost=4620.98..88172.74 rows=10000 width=46) (actual time=59.760..83.317 rows=9750.00 loops=1)
                          Hash Cond: (r.id = visible_media.release_id)
                          Buffers: shared hit=31886
                          ->  Hash Left Join  (cost=15.12..354.38 rows=10000 width=64) (actual time=0.051..4.121 rows=10000.00 loops=1)
                                Hash Cond: (r.id = c_year.release_id)
                                Buffers: shared hit=217
                                ->  Seq Scan on releases r  (cost=0.00..313.00 rows=10000 width=44) (actual time=0.011..2.592 rows=10000.00 loops=1)
                                      Buffers: shared hit=213
                                ->  Hash  (cost=15.08..15.08 rows=3 width=20) (actual time=0.034..0.035 rows=0.00 loops=1)
                                      Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                      Buffers: shared hit=4
                                      ->  Bitmap Heap Scan on release_corrections c_year  (cost=7.97..15.08 rows=3 width=20) (actual time=0.034..0.034 rows=0.00 loops=1)
                                            Recheck Cond: (field = 'year'::text)
                                            Buffers: shared hit=4
                                            ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.028..0.028 rows=0.00 loops=1)
                                                  Index Cond: (field = 'year'::text)
                                                  Index Searches: 1
                                                  Buffers: shared hit=4
                          ->  Hash  (cost=4480.86..4480.86 rows=10000 width=16) (actual time=59.585..59.589 rows=9750.00 loops=1)
                                Buckets: 16384  Batches: 1  Memory Usage: 586kB
                                Buffers: shared hit=2419
                                ->  HashAggregate  (cost=4380.86..4480.86 rows=10000 width=16) (actual time=56.997..57.971 rows=9750.00 loops=1)
                                      Group Key: visible_media.release_id
                                      Batches: 1  Memory Usage: 793kB
                                      Buffers: shared hit=2419
                                      ->  Hash Join  (cost=319.00..4143.41 rows=94977 width=16) (actual time=2.188..41.545 rows=95000.00 loops=1)
                                            Hash Cond: (visible_tracks.medium_id = visible_media.id)
                                            Buffers: shared hit=2419
                                            ->  Seq Scan on tracks visible_tracks  (cost=0.00..3575.00 rows=94977 width=16) (actual time=0.009..21.567 rows=95000.00 loops=1)
                                                  Filter: (source_status = 'present'::text)
                                                  Rows Removed by Filter: 5000
                                                  Buffers: shared hit=2325
                                            ->  Hash  (cost=194.00..194.00 rows=10000 width=32) (actual time=2.112..2.112 rows=10000.00 loops=1)
                                                  Buckets: 16384  Batches: 1  Memory Usage: 753kB
                                                  Buffers: shared hit=94
                                                  ->  Seq Scan on media visible_media  (cost=0.00..194.00 rows=10000 width=32) (actual time=0.006..0.807 rows=10000.00 loops=1)
                                                        Buffers: shared hit=94
                          SubPlan 1
                            ->  Index Scan using release_groups_pkey on release_groups default_group  (cost=0.28..8.30 rows=1 width=16) (actual time=0.001..0.001 rows=1.00 loops=9750)
                                  Index Cond: (id = r.group_id)
                                  Index Searches: 9750
                                  Buffers: shared hit=29250
Planning:
  Buffers: shared hit=406
Planning Time: 3.548 ms
Execution Time: 93.429 ms
```

参数：`[50 0]`；事务jit=off。

```text
Limit  (cost=256490.61..272351.47 rows=50 width=364) (actual time=134.488..144.357 rows=50.00 loops=1)
  Buffers: shared hit=36115
  ->  Result  (cost=256490.61..272351.47 rows=50 width=364) (actual time=134.486..144.342 rows=50.00 loops=1)
        Buffers: shared hit=36115
        ->  Sort  (cost=256490.61..256490.74 rows=50 width=348) (actual time=133.861..133.896 rows=50.00 loops=1)
              Sort Key: ranked.added_at DESC, ranked.group_id DESC
              Sort Method: top-N heapsort  Memory: 46kB
              Buffers: shared hit=32099
              ->  Nested Loop Left Join  (cost=89399.26..256489.20 rows=50 width=348) (actual time=105.477..130.576 rows=7800.00 loops=1)
                    Join Filter: (release_artwork.release_id = r.id)
                    Buffers: shared hit=32099
                    ->  Hash Join  (cost=89399.26..256149.95 rows=50 width=262) (actual time=105.453..125.528 rows=7800.00 loops=1)
                          Hash Cond: (ranked.id = r.id)
                          Buffers: shared hit=32099
                          ->  Subquery Scan on ranked  (cost=88837.12..255587.12 rows=50 width=48) (actual time=96.535..112.771 rows=7800.00 loops=1)
                                Filter: (ranked.member_rank = 1)
                                Buffers: shared hit=31883
                                ->  WindowAgg  (cost=88837.12..255462.12 rows=10000 width=70) (actual time=96.533..111.805 rows=7800.00 loops=1)
                                      Window: w2 AS (PARTITION BY r_1.group_id)
                                      Filter: ((row_number() OVER w1) <= 1)
                                      Rows Removed by Filter: 1950
                                      Storage: Memory  Maximum Storage: 17kB
                                      Buffers: shared hit=31883
                                      ->  WindowAgg  (cost=88837.12..172212.12 rows=10000 width=54) (actual time=96.512..103.428 rows=9750.00 loops=1)
                                            Window: w1 AS (PARTITION BY r_1.group_id ORDER BY ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id ROWS UNBOUNDED PRECEDING)
                                            Run Condition: (row_number() OVER w1 <= 1)
                                            Storage: Memory  Maximum Storage: 17kB
                                            Buffers: shared hit=31883
                                            ->  Sort  (cost=88837.12..88862.12 rows=10000 width=46) (actual time=96.498..98.178 rows=9750.00 loops=1)
                                                  Sort Key: r_1.group_id, ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id
                                                  Sort Method: quicksort  Memory: 994kB
                                                  Buffers: shared hit=31883
                                                  ->  Hash Join  (cost=4620.98..88172.74 rows=10000 width=46) (actual time=64.312..92.253 rows=9750.00 loops=1)
                                                        Hash Cond: (r_1.id = visible_media.release_id)
                                                        Buffers: shared hit=31883
                                                        ->  Hash Left Join  (cost=15.12..354.38 rows=10000 width=64) (actual time=0.046..4.787 rows=10000.00 loops=1)
                                                              Hash Cond: (r_1.id = c_year_1.release_id)
                                                              Buffers: shared hit=214
                                                              ->  Seq Scan on releases r_1  (cost=0.00..313.00 rows=10000 width=44) (actual time=0.015..3.002 rows=10000.00 loops=1)
                                                                    Buffers: shared hit=213
                                                              ->  Hash  (cost=15.08..15.08 rows=3 width=20) (actual time=0.018..0.020 rows=0.00 loops=1)
                                                                    Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                                    Buffers: shared hit=1
                                                                    ->  Bitmap Heap Scan on release_corrections c_year_1  (cost=7.97..15.08 rows=3 width=20) (actual time=0.018..0.019 rows=0.00 loops=1)
                                                                          Recheck Cond: (field = 'year'::text)
                                                                          Buffers: shared hit=1
                                                                          ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.011..0.011 rows=0.00 loops=1)
                                                                                Index Cond: (field = 'year'::text)
                                                                                Index Searches: 1
                                                                                Buffers: shared hit=1
                                                        ->  Hash  (cost=4480.86..4480.86 rows=10000 width=16) (actual time=64.150..64.153 rows=9750.00 loops=1)
                                                              Buckets: 16384  Batches: 1  Memory Usage: 586kB
                                                              Buffers: shared hit=2419
                                                              ->  HashAggregate  (cost=4380.86..4480.86 rows=10000 width=16) (actual time=61.365..62.404 rows=9750.00 loops=1)
                                                                    Group Key: visible_media.release_id
                                                                    Batches: 1  Memory Usage: 793kB
                                                                    Buffers: shared hit=2419
                                                                    ->  Hash Join  (cost=319.00..4143.41 rows=94977 width=16) (actual time=2.600..44.448 rows=95000.00 loops=1)
                                                                          Hash Cond: (visible_tracks.medium_id = visible_media.id)
                                                                          Buffers: shared hit=2419
                                                                          ->  Seq Scan on tracks visible_tracks  (cost=0.00..3575.00 rows=94977 width=16) (actual time=0.013..23.229 rows=95000.00 loops=1)
                                                                                Filter: (source_status = 'present'::text)
                                                                                Rows Removed by Filter: 5000
                                                                                Buffers: shared hit=2325
                                                                          ->  Hash  (cost=194.00..194.00 rows=10000 width=32) (actual time=2.506..2.507 rows=10000.00 loops=1)
                                                                                Buckets: 16384  Batches: 1  Memory Usage: 753kB
                                                                                Buffers: shared hit=94
                                                                                ->  Seq Scan on media visible_media  (cost=0.00..194.00 rows=10000 width=32) (actual time=0.007..0.844 rows=10000.00 loops=1)
                                                                                      Buffers: shared hit=94
                                                        SubPlan 12
                                                          ->  Index Scan using release_groups_pkey on release_groups default_group  (cost=0.28..8.30 rows=1 width=16) (actual time=0.001..0.002 rows=1.00 loops=9750)
                                                                Index Cond: (id = r_1.group_id)
                                                                Index Searches: 9750
                                                                Buffers: shared hit=29250
                          ->  Hash  (cost=437.14..437.14 rows=10000 width=230) (actual time=8.847..8.855 rows=10000.00 loops=1)
                                Buckets: 16384  Batches: 1  Memory Usage: 1027kB
                                Buffers: shared hit=216
                                ->  Hash Left Join  (cost=45.36..437.14 rows=10000 width=230) (actual time=0.086..6.379 rows=10000.00 loops=1)
                                      Hash Cond: (r.id = c_year.release_id)
                                      Buffers: shared hit=216
                                      ->  Hash Left Join  (cost=30.24..395.76 rows=10000 width=210) (actual time=0.064..4.727 rows=10000.00 loops=1)
                                            Hash Cond: (r.id = c_album_artist.release_id)
                                            Buffers: shared hit=215
                                            ->  Hash Left Join  (cost=15.12..354.38 rows=10000 width=178) (actual time=0.048..3.304 rows=10000.00 loops=1)
                                                  Hash Cond: (r.id = c_title.release_id)
                                                  Buffers: shared hit=214
                                                  ->  Seq Scan on releases r  (cost=0.00..313.00 rows=10000 width=146) (actual time=0.020..1.936 rows=10000.00 loops=1)
                                                        Buffers: shared hit=213
                                                  ->  Hash  (cost=15.08..15.08 rows=3 width=48) (actual time=0.013..0.014 rows=0.00 loops=1)
                                                        Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                        Buffers: shared hit=1
                                                        ->  Bitmap Heap Scan on release_corrections c_title  (cost=7.97..15.08 rows=3 width=48) (actual time=0.013..0.014 rows=0.00 loops=1)
                                                              Recheck Cond: (field = 'title'::text)
                                                              Buffers: shared hit=1
                                                              ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.008..0.008 rows=0.00 loops=1)
                                                                    Index Cond: (field = 'title'::text)
                                                                    Index Searches: 1
                                                                    Buffers: shared hit=1
                                            ->  Hash  (cost=15.08..15.08 rows=3 width=48) (actual time=0.006..0.007 rows=0.00 loops=1)
                                                  Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                  Buffers: shared hit=1
                                                  ->  Bitmap Heap Scan on release_corrections c_album_artist  (cost=7.97..15.08 rows=3 width=48) (actual time=0.006..0.006 rows=0.00 loops=1)
                                                        Recheck Cond: (field = 'album_artist'::text)
                                                        Buffers: shared hit=1
                                                        ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.003..0.003 rows=0.00 loops=1)
                                                              Index Cond: (field = 'album_artist'::text)
                                                              Index Searches: 1
                                                              Buffers: shared hit=1
                                      ->  Hash  (cost=15.08..15.08 rows=3 width=20) (actual time=0.011..0.012 rows=0.00 loops=1)
                                            Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                            Buffers: shared hit=1
                                            ->  Bitmap Heap Scan on release_corrections c_year  (cost=7.97..15.08 rows=3 width=20) (actual time=0.010..0.011 rows=0.00 loops=1)
                                                  Recheck Cond: (field = 'year'::text)
                                                  Buffers: shared hit=1
                                                  ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.001..0.001 rows=0.00 loops=1)
                                                        Index Cond: (field = 'year'::text)
                                                        Index Searches: 1
                                                        Buffers: shared hit=1
                    ->  Materialize  (cost=0.00..16.45 rows=430 width=88) (actual time=0.000..0.000 rows=0.00 loops=7800)
                          Storage: Memory  Maximum Storage: 17kB
                          ->  Seq Scan on release_artworks release_artwork  (cost=0.00..14.30 rows=430 width=88) (actual time=0.011..0.011 rows=0.00 loops=1)
        SubPlan 1
          ->  Aggregate  (cost=51.27..51.28 rows=1 width=8) (actual time=0.055..0.055 rows=1.00 loops=50)
                Buffers: shared hit=800
                ->  Sort  (cost=51.22..51.25 rows=9 width=16) (actual time=0.047..0.048 rows=9.74 loops=50)
                      Sort Key: present_medium.id
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=800
                      ->  Nested Loop  (cost=4.78..51.08 rows=9 width=16) (actual time=0.031..0.039 rows=9.74 loops=50)
                            Buffers: shared hit=800
                            ->  Index Scan using media_release_position_uq on media present_medium  (cost=0.29..8.30 rows=1 width=16) (actual time=0.005..0.005 rows=1.00 loops=50)
                                  Index Cond: (release_id = r.id)
                                  Index Searches: 50
                                  Buffers: shared hit=150
                            ->  Bitmap Heap Scan on tracks present_track  (cost=4.50..42.68 rows=10 width=16) (actual time=0.017..0.024 rows=9.74 loops=50)
                                  Recheck Cond: (present_medium.id = medium_id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Heap Blocks: exact=500
                                  Buffers: shared hit=650
                                  ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.013..0.013 rows=10.00 loops=50)
                                        Index Cond: (medium_id = present_medium.id)
                                        Index Searches: 50
                                        Buffers: shared hit=150
        SubPlan 2
          ->  Aggregate  (cost=51.10..51.11 rows=1 width=8) (actual time=0.034..0.034 rows=1.00 loops=50)
                Buffers: shared hit=800
                ->  Nested Loop  (cost=4.78..51.08 rows=9 width=0) (actual time=0.025..0.032 rows=9.74 loops=50)
                      Buffers: shared hit=800
                      ->  Index Scan using media_release_position_uq on media present_medium_1  (cost=0.29..8.30 rows=1 width=16) (actual time=0.003..0.003 rows=1.00 loops=50)
                            Index Cond: (release_id = r.id)
                            Index Searches: 50
                            Buffers: shared hit=150
                      ->  Bitmap Heap Scan on tracks present_track_1  (cost=4.50..42.68 rows=10 width=16) (actual time=0.014..0.020 rows=9.74 loops=50)
                            Recheck Cond: (present_medium_1.id = medium_id)
                            Filter: (source_status = 'present'::text)
                            Rows Removed by Filter: 0
                            Heap Blocks: exact=500
                            Buffers: shared hit=650
                            ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.010..0.010 rows=10.00 loops=50)
                                  Index Cond: (medium_id = present_medium_1.id)
                                  Index Searches: 50
                                  Buffers: shared hit=150
        SubPlan 3
          ->  Aggregate  (cost=24.53..24.54 rows=1 width=8) (actual time=0.003..0.003 rows=1.00 loops=50)
                Buffers: shared hit=100
                ->  Nested Loop Anti Join  (cost=0.44..24.53 rows=1 width=32) (actual time=0.002..0.002 rows=0.00 loops=50)
                      Buffers: shared hit=100
                      ->  Nested Loop Anti Join  (cost=0.29..16.35 rows=1 width=144) (actual time=0.002..0.002 rows=0.00 loops=50)
                            Buffers: shared hit=100
                            ->  Index Scan using release_field_decisions_release_id_field_key_key on release_field_decisions decision  (cost=0.14..8.16 rows=1 width=144) (actual time=0.001..0.001 rows=0.00 loops=50)
                                  Index Cond: (release_id = r.id)
                                  Filter: (action = 'uncertain_apply'::text)
                                  Index Searches: 50
                                  Buffers: shared hit=100
                            ->  Index Only Scan using release_corrections_pkey on release_corrections correction  (cost=0.15..8.17 rows=1 width=48) (never executed)
                                  Index Cond: ((release_id = r.id) AND (field = CASE decision.field_key WHEN 'artist'::text THEN 'album_artist'::text ELSE decision.field_key END))
                                  Heap Fetches: 0
                                  Index Searches: 0
                      ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review  (cost=0.15..8.18 rows=1 width=80) (never executed)
                            Index Cond: ((release_id = r.id) AND (issue = decision.field_key))
                            Filter: (basis = jsonb_build_object('reason', decision.reason, 'value', decision.selected_value, 'candidates', decision.candidates))
                            Index Searches: 0
        SubPlan 5
          ->  Hash Anti Join  (cost=16.18..37.69 rows=378 width=16) (actual time=0.006..0.007 rows=0.00 loops=1)
                Hash Cond: (("grouping".release_id = review_1.release_id) AND (jsonb_build_object('reason', "grouping".reason, 'source_refs', "grouping".source_refs) = review_1.basis))
                ->  Seq Scan on release_grouping_evidence "grouping"  (cost=0.00..15.70 rows=378 width=80) (actual time=0.006..0.006 rows=0.00 loops=1)
                      Filter: (NULLIF(btrim(reason), ''::text) IS NOT NULL)
                ->  Hash  (cost=16.13..16.13 rows=3 width=48) (never executed)
                      ->  Bitmap Heap Scan on release_issue_reviews review_1  (cost=9.03..16.13 rows=3 width=48) (never executed)
                            Recheck Cond: (issue = 'grouping'::text)
                            ->  Bitmap Index Scan on release_issue_reviews_pkey  (cost=0.00..9.03 rows=3 width=0) (never executed)
                                  Index Cond: (issue = 'grouping'::text)
                                  Index Searches: 0
        SubPlan 6
          ->  Aggregate  (cost=59.22..59.23 rows=1 width=8) (actual time=0.043..0.043 rows=1.00 loops=50)
                Buffers: shared hit=826
                ->  Sort  (cost=59.20..59.21 rows=1 width=69) (actual time=0.038..0.038 rows=0.26 loops=50)
                      Sort Key: (COALESCE(NULLIF(missing_track.source_identity, ''::text), (((missing_track.source_root_id)::text || ':'::text) || missing_track.relative_path)))
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=826
                      ->  Nested Loop Anti Join  (cost=4.93..59.19 rows=1 width=69) (actual time=0.030..0.032 rows=0.26 loops=50)
                            Join Filter: (((review_2.basis -> 'tracks'::text) -> (missing_track.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track.observed_at) * '1000000'::numeric))::bigint))
                            Buffers: shared hit=826
                            ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.029..0.031 rows=0.26 loops=50)
                                  Buffers: shared hit=800
                                  ->  Index Scan using media_release_position_uq on media missing_medium  (cost=0.29..8.30 rows=1 width=32) (actual time=0.003..0.003 rows=1.00 loops=50)
                                        Index Cond: (release_id = r.id)
                                        Index Searches: 50
                                        Buffers: shared hit=150
                                  ->  Bitmap Heap Scan on tracks missing_track  (cost=4.49..42.68 rows=1 width=109) (actual time=0.018..0.020 rows=0.26 loops=50)
                                        Recheck Cond: (missing_medium.id = medium_id)
                                        Filter: (source_status = 'missing'::text)
                                        Rows Removed by Filter: 10
                                        Heap Blocks: exact=500
                                        Buffers: shared hit=650
                                        ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.010..0.010 rows=10.00 loops=50)
                                              Index Cond: (medium_id = missing_medium.id)
                                              Index Searches: 50
                                              Buffers: shared hit=150
                            ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review_2  (cost=0.15..8.17 rows=1 width=48) (actual time=0.002..0.002 rows=0.00 loops=13)
                                  Index Cond: ((release_id = r.id) AND (issue = 'missing_tracks'::text))
                                  Index Searches: 13
                                  Buffers: shared hit=26
        SubPlan 11
          ->  Aggregate  (cost=114.64..114.65 rows=1 width=8) (actual time=0.067..0.067 rows=1.00 loops=50)
                Buffers: shared hit=1490
                ->  Nested Loop Semi Join  (cost=0.99..14.50 rows=1 width=16) (actual time=0.013..0.013 rows=1.00 loops=50)
                      Buffers: shared hit=464
                      ->  Index Only Scan using releases_group_created_id_index on releases r_2  (cost=0.29..4.30 rows=1 width=16) (actual time=0.005..0.005 rows=1.00 loops=50)
                            Index Cond: (group_id = ranked.group_id)
                            Heap Fetches: 0
                            Index Searches: 50
                            Buffers: shared hit=101
                      ->  Nested Loop  (cost=0.70..10.18 rows=9 width=16) (actual time=0.007..0.007 rows=1.00 loops=50)
                            Buffers: shared hit=363
                            ->  Index Scan using media_release_position_uq on media visible_media_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.003..0.003 rows=1.00 loops=50)
                                  Index Cond: (release_id = r_2.id)
                                  Index Searches: 50
                                  Buffers: shared hit=150
                            ->  Index Scan using tracks_release_index on tracks visible_tracks_1  (cost=0.42..1.78 rows=10 width=16) (actual time=0.003..0.003 rows=1.00 loops=50)
                                  Index Cond: (medium_id = visible_media_1.id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Index Searches: 50
                                  Buffers: shared hit=213
                SubPlan 7
                  ->  Aggregate  (cost=24.53..24.54 rows=1 width=8) (actual time=0.003..0.003 rows=1.00 loops=50)
                        Buffers: shared hit=100
                        ->  Nested Loop Anti Join  (cost=0.44..24.53 rows=1 width=32) (actual time=0.002..0.002 rows=0.00 loops=50)
                              Buffers: shared hit=100
                              ->  Nested Loop Anti Join  (cost=0.29..16.35 rows=1 width=144) (actual time=0.002..0.002 rows=0.00 loops=50)
                                    Buffers: shared hit=100
                                    ->  Index Scan using release_field_decisions_release_id_field_key_key on release_field_decisions decision_1  (cost=0.14..8.16 rows=1 width=144) (actual time=0.001..0.001 rows=0.00 loops=50)
                                          Index Cond: (release_id = r_2.id)
                                          Filter: (action = 'uncertain_apply'::text)
                                          Index Searches: 50
                                          Buffers: shared hit=100
                                    ->  Index Only Scan using release_corrections_pkey on release_corrections correction_1  (cost=0.15..8.17 rows=1 width=48) (never executed)
                                          Index Cond: ((release_id = r_2.id) AND (field = CASE decision_1.field_key WHEN 'artist'::text THEN 'album_artist'::text ELSE decision_1.field_key END))
                                          Heap Fetches: 0
                                          Index Searches: 0
                              ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review_3  (cost=0.15..8.18 rows=1 width=80) (never executed)
                                    Index Cond: ((release_id = r_2.id) AND (issue = decision_1.field_key))
                                    Filter: (basis = jsonb_build_object('reason', decision_1.reason, 'value', decision_1.selected_value, 'candidates', decision_1.candidates))
                                    Index Searches: 0
                SubPlan 8
                  ->  Nested Loop Anti Join  (cost=0.30..16.36 rows=1 width=0) (actual time=0.001..0.001 rows=0.00 loops=50)
                        Join Filter: (review_4.basis = jsonb_build_object('reason', grouping_1.reason, 'source_refs', grouping_1.source_refs))
                        Buffers: shared hit=100
                        ->  Index Scan using release_grouping_evidence_current_uq on release_grouping_evidence grouping_1  (cost=0.15..8.17 rows=1 width=80) (actual time=0.001..0.001 rows=0.00 loops=50)
                              Index Cond: (release_id = r_2.id)
                              Filter: (NULLIF(btrim(reason), ''::text) IS NOT NULL)
                              Index Searches: 50
                              Buffers: shared hit=100
                        ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review_4  (cost=0.15..8.17 rows=1 width=48) (never executed)
                              Index Cond: ((release_id = r_2.id) AND (issue = 'grouping'::text))
                              Index Searches: 0
                SubPlan 10
                  ->  Aggregate  (cost=59.22..59.23 rows=1 width=8) (actual time=0.045..0.046 rows=1.00 loops=50)
                        Buffers: shared hit=826
                        ->  Sort  (cost=59.20..59.21 rows=1 width=69) (actual time=0.039..0.039 rows=0.26 loops=50)
                              Sort Key: (COALESCE(NULLIF(missing_track_1.source_identity, ''::text), (((missing_track_1.source_root_id)::text || ':'::text) || missing_track_1.relative_path)))
                              Sort Method: quicksort  Memory: 25kB
                              Buffers: shared hit=826
                              ->  Nested Loop Anti Join  (cost=4.93..59.19 rows=1 width=69) (actual time=0.031..0.033 rows=0.26 loops=50)
                                    Join Filter: (((review_5.basis -> 'tracks'::text) -> (missing_track_1.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track_1.observed_at) * '1000000'::numeric))::bigint))
                                    Buffers: shared hit=826
                                    ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.030..0.031 rows=0.26 loops=50)
                                          Buffers: shared hit=800
                                          ->  Index Scan using media_release_position_uq on media missing_medium_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.003..0.003 rows=1.00 loops=50)
                                                Index Cond: (release_id = r_2.id)
                                                Index Searches: 50
                                                Buffers: shared hit=150
                                          ->  Bitmap Heap Scan on tracks missing_track_1  (cost=4.49..42.68 rows=1 width=109) (actual time=0.019..0.020 rows=0.26 loops=50)
                                                Recheck Cond: (missing_medium_1.id = medium_id)
                                                Filter: (source_status = 'missing'::text)
                                                Rows Removed by Filter: 10
                                                Heap Blocks: exact=500
                                                Buffers: shared hit=650
                                                ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.011..0.011 rows=10.00 loops=50)
                                                      Index Cond: (medium_id = missing_medium_1.id)
                                                      Index Searches: 50
                                                      Buffers: shared hit=150
                                    ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review_5  (cost=0.15..8.17 rows=1 width=48) (actual time=0.003..0.003 rows=0.00 loops=13)
                                          Index Cond: ((release_id = r_2.id) AND (issue = 'missing_tracks'::text))
                                          Index Searches: 13
                                          Buffers: shared hit=26
Planning:
  Buffers: shared hit=478
Planning Time: 8.063 ms
Execution Time: 146.274 ms
```

## 辅助空表已 ANALYZE

- `release_corrections`: reltuples=0。
- `release_field_decisions`: reltuples=0。
- `release_grouping_evidence`: reltuples=0。
- `release_issue_reviews`: reltuples=0。
- `release_artworks`: reltuples=0。

### 最近8项（无COUNT）

参数：`[]`；事务jit=off。

```text
Limit  (cost=256092.52..257846.38 rows=8 width=364) (actual time=152.009..153.469 rows=8.00 loops=1)
  Buffers: shared hit=32682
  ->  Result  (cost=256092.52..267054.14 rows=50 width=364) (actual time=152.008..153.453 rows=8.00 loops=1)
        Buffers: shared hit=32682
        ->  Sort  (cost=256092.52..256092.65 rows=50 width=348) (actual time=151.539..151.557 rows=8.00 loops=1)
              Sort Key: ranked.added_at DESC, ranked.group_id DESC
              Sort Method: top-N heapsort  Memory: 28kB
              Buffers: shared hit=32095
              ->  Nested Loop Left Join  (cost=89338.84..256091.52 rows=50 width=348) (actual time=112.505..147.299 rows=7800.00 loops=1)
                    Join Filter: (release_artwork.release_id = r.id)
                    Buffers: shared hit=32095
                    ->  Hash Join  (cost=89338.84..256089.52 rows=50 width=262) (actual time=112.486..137.351 rows=7800.00 loops=1)
                          Hash Cond: (ranked.id = r.id)
                          Buffers: shared hit=32095
                          ->  Subquery Scan on ranked  (cost=88822.02..255572.02 rows=50 width=48) (actual time=100.490..120.714 rows=7800.00 loops=1)
                                Filter: (ranked.member_rank = 1)
                                Buffers: shared hit=31882
                                ->  WindowAgg  (cost=88822.02..255447.02 rows=10000 width=70) (actual time=100.489..119.387 rows=7800.00 loops=1)
                                      Window: w2 AS (PARTITION BY r_1.group_id)
                                      Filter: ((row_number() OVER w1) <= 1)
                                      Rows Removed by Filter: 1950
                                      Storage: Memory  Maximum Storage: 17kB
                                      Buffers: shared hit=31882
                                      ->  WindowAgg  (cost=88822.02..172197.02 rows=10000 width=54) (actual time=100.468..108.851 rows=9750.00 loops=1)
                                            Window: w1 AS (PARTITION BY r_1.group_id ORDER BY ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id ROWS UNBOUNDED PRECEDING)
                                            Run Condition: (row_number() OVER w1 <= 1)
                                            Storage: Memory  Maximum Storage: 17kB
                                            Buffers: shared hit=31882
                                            ->  Sort  (cost=88822.02..88847.02 rows=10000 width=46) (actual time=100.452..102.308 rows=9750.00 loops=1)
                                                  Sort Key: r_1.group_id, ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id
                                                  Sort Method: quicksort  Memory: 994kB
                                                  Buffers: shared hit=31882
                                                  ->  Hash Join  (cost=4605.87..88157.63 rows=10000 width=46) (actual time=68.594..95.885 rows=9750.00 loops=1)
                                                        Hash Cond: (r_1.id = visible_media.release_id)
                                                        Buffers: shared hit=31882
                                                        ->  Hash Left Join  (cost=0.01..339.27 rows=10000 width=64) (actual time=0.028..4.651 rows=10000.00 loops=1)
                                                              Hash Cond: (r_1.id = c_year_1.release_id)
                                                              Buffers: shared hit=213
                                                              ->  Seq Scan on releases r_1  (cost=0.00..313.00 rows=10000 width=44) (actual time=0.011..2.918 rows=10000.00 loops=1)
                                                                    Buffers: shared hit=213
                                                              ->  Hash  (cost=0.00..0.00 rows=1 width=20) (actual time=0.003..0.004 rows=0.00 loops=1)
                                                                    Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                                    ->  Seq Scan on release_corrections c_year_1  (cost=0.00..0.00 rows=1 width=20) (actual time=0.003..0.003 rows=0.00 loops=1)
                                                                          Filter: (field = 'year'::text)
                                                        ->  Hash  (cost=4480.86..4480.86 rows=10000 width=16) (actual time=68.439..68.442 rows=9750.00 loops=1)
                                                              Buckets: 16384  Batches: 1  Memory Usage: 586kB
                                                              Buffers: shared hit=2419
                                                              ->  HashAggregate  (cost=4380.86..4480.86 rows=10000 width=16) (actual time=64.778..66.180 rows=9750.00 loops=1)
                                                                    Group Key: visible_media.release_id
                                                                    Batches: 1  Memory Usage: 793kB
                                                                    Buffers: shared hit=2419
                                                                    ->  Hash Join  (cost=319.00..4143.41 rows=94977 width=16) (actual time=2.756..46.693 rows=95000.00 loops=1)
                                                                          Hash Cond: (visible_tracks.medium_id = visible_media.id)
                                                                          Buffers: shared hit=2419
                                                                          ->  Seq Scan on tracks visible_tracks  (cost=0.00..3575.00 rows=94977 width=16) (actual time=0.015..24.373 rows=95000.00 loops=1)
                                                                                Filter: (source_status = 'present'::text)
                                                                                Rows Removed by Filter: 5000
                                                                                Buffers: shared hit=2325
                                                                          ->  Hash  (cost=194.00..194.00 rows=10000 width=32) (actual time=2.665..2.666 rows=10000.00 loops=1)
                                                                                Buckets: 16384  Batches: 1  Memory Usage: 753kB
                                                                                Buffers: shared hit=94
                                                                                ->  Seq Scan on media visible_media  (cost=0.00..194.00 rows=10000 width=32) (actual time=0.007..0.885 rows=10000.00 loops=1)
                                                                                      Buffers: shared hit=94
                                                        SubPlan 12
                                                          ->  Index Scan using release_groups_pkey on release_groups default_group  (cost=0.28..8.30 rows=1 width=16) (actual time=0.001..0.002 rows=1.00 loops=9750)
                                                                Index Cond: (id = r_1.group_id)
                                                                Index Searches: 9750
                                                                Buffers: shared hit=29250
                          ->  Hash  (cost=391.82..391.82 rows=10000 width=230) (actual time=11.893..11.899 rows=10000.00 loops=1)
                                Buckets: 16384  Batches: 1  Memory Usage: 1027kB
                                Buffers: shared hit=213
                                ->  Hash Left Join  (cost=0.04..391.82 rows=10000 width=230) (actual time=0.086..8.470 rows=10000.00 loops=1)
                                      Hash Cond: (r.id = c_year.release_id)
                                      Buffers: shared hit=213
                                      ->  Hash Left Join  (cost=0.03..365.55 rows=10000 width=210) (actual time=0.066..6.476 rows=10000.00 loops=1)
                                            Hash Cond: (r.id = c_album_artist.release_id)
                                            Buffers: shared hit=213
                                            ->  Hash Left Join  (cost=0.01..339.27 rows=10000 width=178) (actual time=0.047..4.654 rows=10000.00 loops=1)
                                                  Hash Cond: (r.id = c_title.release_id)
                                                  Buffers: shared hit=213
                                                  ->  Seq Scan on releases r  (cost=0.00..313.00 rows=10000 width=146) (actual time=0.017..2.840 rows=10000.00 loops=1)
                                                        Buffers: shared hit=213
                                                  ->  Hash  (cost=0.00..0.00 rows=1 width=48) (actual time=0.005..0.006 rows=0.00 loops=1)
                                                        Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                        ->  Seq Scan on release_corrections c_title  (cost=0.00..0.00 rows=1 width=48) (actual time=0.005..0.005 rows=0.00 loops=1)
                                                              Filter: (field = 'title'::text)
                                            ->  Hash  (cost=0.00..0.00 rows=1 width=48) (actual time=0.001..0.002 rows=0.00 loops=1)
                                                  Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                  ->  Seq Scan on release_corrections c_album_artist  (cost=0.00..0.00 rows=1 width=48) (actual time=0.001..0.001 rows=0.00 loops=1)
                                                        Filter: (field = 'album_artist'::text)
                                      ->  Hash  (cost=0.00..0.00 rows=1 width=20) (actual time=0.004..0.005 rows=0.00 loops=1)
                                            Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                            ->  Seq Scan on release_corrections c_year  (cost=0.00..0.00 rows=1 width=20) (actual time=0.004..0.004 rows=0.00 loops=1)
                                                  Filter: (field = 'year'::text)
                    ->  Seq Scan on release_artworks release_artwork  (cost=0.00..0.00 rows=1 width=88) (actual time=0.000..0.000 rows=0.00 loops=7800)
        SubPlan 1
          ->  Aggregate  (cost=51.27..51.28 rows=1 width=8) (actual time=0.064..0.064 rows=1.00 loops=8)
                Buffers: shared hit=128
                ->  Sort  (cost=51.22..51.25 rows=9 width=16) (actual time=0.054..0.055 rows=9.75 loops=8)
                      Sort Key: present_medium.id
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=128
                      ->  Nested Loop  (cost=4.78..51.08 rows=9 width=16) (actual time=0.034..0.041 rows=9.75 loops=8)
                            Buffers: shared hit=128
                            ->  Index Scan using media_release_position_uq on media present_medium  (cost=0.29..8.30 rows=1 width=16) (actual time=0.008..0.008 rows=1.00 loops=8)
                                  Index Cond: (release_id = r.id)
                                  Index Searches: 8
                                  Buffers: shared hit=24
                            ->  Bitmap Heap Scan on tracks present_track  (cost=4.50..42.68 rows=10 width=16) (actual time=0.019..0.025 rows=9.75 loops=8)
                                  Recheck Cond: (present_medium.id = medium_id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Heap Blocks: exact=80
                                  Buffers: shared hit=104
                                  ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.014..0.014 rows=10.00 loops=8)
                                        Index Cond: (medium_id = present_medium.id)
                                        Index Searches: 8
                                        Buffers: shared hit=24
        SubPlan 2
          ->  Aggregate  (cost=51.10..51.11 rows=1 width=8) (actual time=0.031..0.032 rows=1.00 loops=8)
                Buffers: shared hit=128
                ->  Nested Loop  (cost=4.78..51.08 rows=9 width=0) (actual time=0.024..0.030 rows=9.75 loops=8)
                      Buffers: shared hit=128
                      ->  Index Scan using media_release_position_uq on media present_medium_1  (cost=0.29..8.30 rows=1 width=16) (actual time=0.003..0.003 rows=1.00 loops=8)
                            Index Cond: (release_id = r.id)
                            Index Searches: 8
                            Buffers: shared hit=24
                      ->  Bitmap Heap Scan on tracks present_track_1  (cost=4.50..42.68 rows=10 width=16) (actual time=0.014..0.019 rows=9.75 loops=8)
                            Recheck Cond: (present_medium_1.id = medium_id)
                            Filter: (source_status = 'present'::text)
                            Rows Removed by Filter: 0
                            Heap Blocks: exact=80
                            Buffers: shared hit=104
                            ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.011..0.011 rows=10.00 loops=8)
                                  Index Cond: (medium_id = present_medium_1.id)
                                  Index Searches: 8
                                  Buffers: shared hit=24
        SubPlan 3
          ->  Aggregate  (cost=0.05..0.06 rows=1 width=8) (actual time=0.017..0.017 rows=1.00 loops=8)
                ->  Sort  (cost=0.04..0.05 rows=1 width=32) (actual time=0.010..0.010 rows=0.00 loops=8)
                      Sort Key: decision.field_key
                      Sort Method: quicksort  Memory: 25kB
                      ->  Nested Loop Anti Join  (cost=0.00..0.03 rows=1 width=32) (actual time=0.003..0.003 rows=0.00 loops=8)
                            Join Filter: ((review.issue = decision.field_key) AND (review.basis = jsonb_build_object('reason', decision.reason, 'value', decision.selected_value, 'candidates', decision.candidates)))
                            ->  Nested Loop Anti Join  (cost=0.00..0.01 rows=1 width=144) (actual time=0.003..0.003 rows=0.00 loops=8)
                                  Join Filter: (correction.field = CASE decision.field_key WHEN 'artist'::text THEN 'album_artist'::text ELSE decision.field_key END)
                                  ->  Seq Scan on release_field_decisions decision  (cost=0.00..0.00 rows=1 width=144) (actual time=0.002..0.002 rows=0.00 loops=8)
                                        Filter: ((release_id = r.id) AND (action = 'uncertain_apply'::text))
                                  ->  Seq Scan on release_corrections correction  (cost=0.00..0.00 rows=1 width=48) (never executed)
                                        Filter: (release_id = r.id)
                            ->  Seq Scan on release_issue_reviews review  (cost=0.00..0.00 rows=1 width=80) (never executed)
                                  Filter: (release_id = r.id)
        SubPlan 5
          ->  Nested Loop Anti Join  (cost=0.00..0.02 rows=1 width=16) (actual time=0.006..0.007 rows=0.00 loops=1)
                Join Filter: ((review_1.release_id = "grouping".release_id) AND (review_1.basis = jsonb_build_object('reason', "grouping".reason, 'source_refs', "grouping".source_refs)))
                ->  Seq Scan on release_grouping_evidence "grouping"  (cost=0.00..0.00 rows=1 width=80) (actual time=0.006..0.006 rows=0.00 loops=1)
                      Filter: (NULLIF(btrim(reason), ''::text) IS NOT NULL)
                ->  Seq Scan on release_issue_reviews review_1  (cost=0.00..0.00 rows=1 width=48) (never executed)
                      Filter: (issue = 'grouping'::text)
        SubPlan 6
          ->  Aggregate  (cost=51.05..51.06 rows=1 width=8) (actual time=0.042..0.043 rows=1.00 loops=8)
                Buffers: shared hit=128
                ->  Sort  (cost=51.03..51.04 rows=1 width=69) (actual time=0.034..0.034 rows=0.25 loops=8)
                      Sort Key: (COALESCE(NULLIF(missing_track.source_identity, ''::text), (((missing_track.source_root_id)::text || ':'::text) || missing_track.relative_path)))
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=128
                      ->  Nested Loop Anti Join  (cost=4.78..51.02 rows=1 width=69) (actual time=0.026..0.027 rows=0.25 loops=8)
                            Join Filter: (((review_2.basis -> 'tracks'::text) -> (missing_track.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track.observed_at) * '1000000'::numeric))::bigint))
                            Buffers: shared hit=128
                            ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.025..0.026 rows=0.25 loops=8)
                                  Buffers: shared hit=128
                                  ->  Index Scan using media_release_position_uq on media missing_medium  (cost=0.29..8.30 rows=1 width=32) (actual time=0.003..0.004 rows=1.00 loops=8)
                                        Index Cond: (release_id = r.id)
                                        Index Searches: 8
                                        Buffers: shared hit=24
                                  ->  Bitmap Heap Scan on tracks missing_track  (cost=4.49..42.68 rows=1 width=109) (actual time=0.015..0.016 rows=0.25 loops=8)
                                        Recheck Cond: (missing_medium.id = medium_id)
                                        Filter: (source_status = 'missing'::text)
                                        Rows Removed by Filter: 10
                                        Heap Blocks: exact=80
                                        Buffers: shared hit=104
                                        ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.009..0.009 rows=10.00 loops=8)
                                              Index Cond: (medium_id = missing_medium.id)
                                              Index Searches: 8
                                              Buffers: shared hit=24
                            ->  Seq Scan on release_issue_reviews review_2  (cost=0.00..0.00 rows=1 width=48) (actual time=0.001..0.001 rows=0.00 loops=2)
                                  Filter: ((release_id = r.id) AND (issue = 'missing_tracks'::text))
        SubPlan 11
          ->  Aggregate  (cost=65.65..65.66 rows=1 width=8) (actual time=0.073..0.074 rows=1.00 loops=8)
                Buffers: shared hit=203
                ->  Nested Loop Semi Join  (cost=0.99..14.50 rows=1 width=16) (actual time=0.013..0.014 rows=1.00 loops=8)
                      Buffers: shared hit=75
                      ->  Index Only Scan using releases_group_created_id_index on releases r_2  (cost=0.29..4.30 rows=1 width=16) (actual time=0.006..0.006 rows=1.00 loops=8)
                            Index Cond: (group_id = ranked.group_id)
                            Heap Fetches: 0
                            Index Searches: 8
                            Buffers: shared hit=17
                      ->  Nested Loop  (cost=0.70..10.18 rows=9 width=16) (actual time=0.006..0.006 rows=1.00 loops=8)
                            Buffers: shared hit=58
                            ->  Index Scan using media_release_position_uq on media visible_media_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.002..0.002 rows=1.00 loops=8)
                                  Index Cond: (release_id = r_2.id)
                                  Index Searches: 8
                                  Buffers: shared hit=24
                            ->  Index Scan using tracks_release_index on tracks visible_tracks_1  (cost=0.42..1.78 rows=10 width=16) (actual time=0.003..0.003 rows=1.00 loops=8)
                                  Index Cond: (medium_id = visible_media_1.id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Index Searches: 8
                                  Buffers: shared hit=34
                SubPlan 7
                  ->  Aggregate  (cost=0.05..0.06 rows=1 width=8) (actual time=0.015..0.015 rows=1.00 loops=8)
                        ->  Sort  (cost=0.04..0.05 rows=1 width=32) (actual time=0.008..0.008 rows=0.00 loops=8)
                              Sort Key: decision_1.field_key
                              Sort Method: quicksort  Memory: 25kB
                              ->  Nested Loop Anti Join  (cost=0.00..0.03 rows=1 width=32) (actual time=0.002..0.002 rows=0.00 loops=8)
                                    Join Filter: ((review_3.issue = decision_1.field_key) AND (review_3.basis = jsonb_build_object('reason', decision_1.reason, 'value', decision_1.selected_value, 'candidates', decision_1.candidates)))
                                    ->  Nested Loop Anti Join  (cost=0.00..0.01 rows=1 width=144) (actual time=0.001..0.001 rows=0.00 loops=8)
                                          Join Filter: (correction_1.field = CASE decision_1.field_key WHEN 'artist'::text THEN 'album_artist'::text ELSE decision_1.field_key END)
                                          ->  Seq Scan on release_field_decisions decision_1  (cost=0.00..0.00 rows=1 width=144) (actual time=0.000..0.000 rows=0.00 loops=8)
                                                Filter: ((release_id = r_2.id) AND (action = 'uncertain_apply'::text))
                                          ->  Seq Scan on release_corrections correction_1  (cost=0.00..0.00 rows=1 width=48) (never executed)
                                                Filter: (release_id = r_2.id)
                                    ->  Seq Scan on release_issue_reviews review_3  (cost=0.00..0.00 rows=1 width=80) (never executed)
                                          Filter: (release_id = r_2.id)
                SubPlan 8
                  ->  Nested Loop Anti Join  (cost=0.00..0.01 rows=1 width=0) (actual time=0.001..0.001 rows=0.00 loops=8)
                        Join Filter: (review_4.basis = jsonb_build_object('reason', grouping_1.reason, 'source_refs', grouping_1.source_refs))
                        ->  Seq Scan on release_grouping_evidence grouping_1  (cost=0.00..0.00 rows=1 width=80) (actual time=0.000..0.000 rows=0.00 loops=8)
                              Filter: ((release_id = r_2.id) AND (NULLIF(btrim(reason), ''::text) IS NOT NULL))
                        ->  Seq Scan on release_issue_reviews review_4  (cost=0.00..0.00 rows=1 width=48) (never executed)
                              Filter: ((release_id = r_2.id) AND (issue = 'grouping'::text))
                SubPlan 10
                  ->  Aggregate  (cost=51.05..51.06 rows=1 width=8) (actual time=0.039..0.039 rows=1.00 loops=8)
                        Buffers: shared hit=128
                        ->  Sort  (cost=51.03..51.04 rows=1 width=69) (actual time=0.032..0.033 rows=0.25 loops=8)
                              Sort Key: (COALESCE(NULLIF(missing_track_1.source_identity, ''::text), (((missing_track_1.source_root_id)::text || ':'::text) || missing_track_1.relative_path)))
                              Sort Method: quicksort  Memory: 25kB
                              Buffers: shared hit=128
                              ->  Nested Loop Anti Join  (cost=4.78..51.02 rows=1 width=69) (actual time=0.025..0.026 rows=0.25 loops=8)
                                    Join Filter: (((review_5.basis -> 'tracks'::text) -> (missing_track_1.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track_1.observed_at) * '1000000'::numeric))::bigint))
                                    Buffers: shared hit=128
                                    ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.024..0.025 rows=0.25 loops=8)
                                          Buffers: shared hit=128
                                          ->  Index Scan using media_release_position_uq on media missing_medium_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.003..0.003 rows=1.00 loops=8)
                                                Index Cond: (release_id = r_2.id)
                                                Index Searches: 8
                                                Buffers: shared hit=24
                                          ->  Bitmap Heap Scan on tracks missing_track_1  (cost=4.49..42.68 rows=1 width=109) (actual time=0.015..0.016 rows=0.25 loops=8)
                                                Recheck Cond: (missing_medium_1.id = medium_id)
                                                Filter: (source_status = 'missing'::text)
                                                Rows Removed by Filter: 10
                                                Heap Blocks: exact=80
                                                Buffers: shared hit=104
                                                ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.009..0.009 rows=10.00 loops=8)
                                                      Index Cond: (medium_id = missing_medium_1.id)
                                                      Index Searches: 8
                                                      Buffers: shared hit=24
                                    ->  Seq Scan on release_issue_reviews review_5  (cost=0.00..0.00 rows=1 width=48) (actual time=0.002..0.002 rows=0.00 loops=2)
                                          Filter: ((release_id = r_2.id) AND (issue = 'missing_tracks'::text))
Planning:
  Buffers: shared hit=180
Planning Time: 5.499 ms
Execution Time: 155.297 ms
```

### 加入时间排序首页50项（含COUNT）

参数：`[]`；事务jit=off。

```text
Aggregate  (cost=172322.14..172322.15 rows=1 width=8) (actual time=93.900..93.907 rows=1.00 loops=1)
  Buffers: shared hit=31882
  ->  Subquery Scan on ranked  (cost=88822.02..172322.02 rows=50 width=0) (actual time=88.252..93.507 rows=7800.00 loops=1)
        Filter: (ranked.member_rank = 1)
        Buffers: shared hit=31882
        ->  WindowAgg  (cost=88822.02..172197.02 rows=10000 width=70) (actual time=88.251..92.805 rows=7800.00 loops=1)
              Window: w1 AS (PARTITION BY r.group_id ORDER BY ((r.id IS DISTINCT FROM (SubPlan 1))), ((NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0)), r.created_at, r.id ROWS UNBOUNDED PRECEDING)
              Run Condition: (row_number() OVER w1 <= 1)
              Storage: Memory  Maximum Storage: 17kB
              Buffers: shared hit=31882
              ->  Sort  (cost=88822.02..88847.02 rows=10000 width=46) (actual time=88.241..89.087 rows=9750.00 loops=1)
                    Sort Key: r.group_id, ((r.id IS DISTINCT FROM (SubPlan 1))), ((NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0)), r.created_at, r.id
                    Sort Method: quicksort  Memory: 994kB
                    Buffers: shared hit=31882
                    ->  Hash Join  (cost=4605.87..88157.63 rows=10000 width=46) (actual time=58.420..84.374 rows=9750.00 loops=1)
                          Hash Cond: (r.id = visible_media.release_id)
                          Buffers: shared hit=31882
                          ->  Hash Left Join  (cost=0.01..339.27 rows=10000 width=64) (actual time=0.020..4.508 rows=10000.00 loops=1)
                                Hash Cond: (r.id = c_year.release_id)
                                Buffers: shared hit=213
                                ->  Seq Scan on releases r  (cost=0.00..313.00 rows=10000 width=44) (actual time=0.013..2.826 rows=10000.00 loops=1)
                                      Buffers: shared hit=213
                                ->  Hash  (cost=0.00..0.00 rows=1 width=20) (actual time=0.003..0.004 rows=0.00 loops=1)
                                      Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                      ->  Seq Scan on release_corrections c_year  (cost=0.00..0.00 rows=1 width=20) (actual time=0.003..0.003 rows=0.00 loops=1)
                                            Filter: (field = 'year'::text)
                          ->  Hash  (cost=4480.86..4480.86 rows=10000 width=16) (actual time=58.298..58.301 rows=9750.00 loops=1)
                                Buckets: 16384  Batches: 1  Memory Usage: 586kB
                                Buffers: shared hit=2419
                                ->  HashAggregate  (cost=4380.86..4480.86 rows=10000 width=16) (actual time=55.849..56.752 rows=9750.00 loops=1)
                                      Group Key: visible_media.release_id
                                      Batches: 1  Memory Usage: 793kB
                                      Buffers: shared hit=2419
                                      ->  Hash Join  (cost=319.00..4143.41 rows=94977 width=16) (actual time=2.315..40.687 rows=95000.00 loops=1)
                                            Hash Cond: (visible_tracks.medium_id = visible_media.id)
                                            Buffers: shared hit=2419
                                            ->  Seq Scan on tracks visible_tracks  (cost=0.00..3575.00 rows=94977 width=16) (actual time=0.008..21.613 rows=95000.00 loops=1)
                                                  Filter: (source_status = 'present'::text)
                                                  Rows Removed by Filter: 5000
                                                  Buffers: shared hit=2325
                                            ->  Hash  (cost=194.00..194.00 rows=10000 width=32) (actual time=2.224..2.224 rows=10000.00 loops=1)
                                                  Buckets: 16384  Batches: 1  Memory Usage: 753kB
                                                  Buffers: shared hit=94
                                                  ->  Seq Scan on media visible_media  (cost=0.00..194.00 rows=10000 width=32) (actual time=0.007..0.838 rows=10000.00 loops=1)
                                                        Buffers: shared hit=94
                          SubPlan 1
                            ->  Index Scan using release_groups_pkey on release_groups default_group  (cost=0.28..8.30 rows=1 width=16) (actual time=0.001..0.002 rows=1.00 loops=9750)
                                  Index Cond: (id = r.group_id)
                                  Index Searches: 9750
                                  Buffers: shared hit=29250
Planning:
  Buffers: shared hit=37
Planning Time: 0.999 ms
Execution Time: 94.530 ms
```

参数：`[50 0]`；事务jit=off。

```text
Limit  (cost=256092.94..267054.55 rows=50 width=364) (actual time=133.538..140.773 rows=50.00 loops=1)
  Buffers: shared hit=35776
  ->  Result  (cost=256092.94..267054.55 rows=50 width=364) (actual time=133.536..140.760 rows=50.00 loops=1)
        Buffers: shared hit=35776
        ->  Sort  (cost=256092.94..256093.06 rows=50 width=348) (actual time=133.007..133.029 rows=50.00 loops=1)
              Sort Key: ranked.added_at DESC, ranked.group_id DESC
              Sort Method: top-N heapsort  Memory: 46kB
              Buffers: shared hit=32109
              ->  Nested Loop Left Join  (cost=89338.84..256091.52 rows=50 width=348) (actual time=103.033..129.838 rows=7800.00 loops=1)
                    Join Filter: (release_artwork.release_id = r.id)
                    Buffers: shared hit=32103
                    ->  Hash Join  (cost=89338.84..256089.52 rows=50 width=262) (actual time=103.012..122.005 rows=7800.00 loops=1)
                          Hash Cond: (ranked.id = r.id)
                          Buffers: shared hit=32103
                          ->  Subquery Scan on ranked  (cost=88822.02..255572.02 rows=50 width=48) (actual time=94.245..109.675 rows=7800.00 loops=1)
                                Filter: (ranked.member_rank = 1)
                                Buffers: shared hit=31890
                                ->  WindowAgg  (cost=88822.02..255447.02 rows=10000 width=70) (actual time=94.244..108.742 rows=7800.00 loops=1)
                                      Window: w2 AS (PARTITION BY r_1.group_id)
                                      Filter: ((row_number() OVER w1) <= 1)
                                      Rows Removed by Filter: 1950
                                      Storage: Memory  Maximum Storage: 17kB
                                      Buffers: shared hit=31890
                                      ->  WindowAgg  (cost=88822.02..172197.02 rows=10000 width=54) (actual time=94.219..100.743 rows=9750.00 loops=1)
                                            Window: w1 AS (PARTITION BY r_1.group_id ORDER BY ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id ROWS UNBOUNDED PRECEDING)
                                            Run Condition: (row_number() OVER w1 <= 1)
                                            Storage: Memory  Maximum Storage: 17kB
                                            Buffers: shared hit=31890
                                            ->  Sort  (cost=88822.02..88847.02 rows=10000 width=46) (actual time=94.200..95.816 rows=9750.00 loops=1)
                                                  Sort Key: r_1.group_id, ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id
                                                  Sort Method: quicksort  Memory: 994kB
                                                  Buffers: shared hit=31890
                                                  ->  Hash Join  (cost=4605.87..88157.63 rows=10000 width=46) (actual time=59.626..89.640 rows=9750.00 loops=1)
                                                        Hash Cond: (r_1.id = visible_media.release_id)
                                                        Buffers: shared hit=31882
                                                        ->  Hash Left Join  (cost=0.01..339.27 rows=10000 width=64) (actual time=0.027..5.150 rows=10000.00 loops=1)
                                                              Hash Cond: (r_1.id = c_year_1.release_id)
                                                              Buffers: shared hit=213
                                                              ->  Seq Scan on releases r_1  (cost=0.00..313.00 rows=10000 width=44) (actual time=0.013..3.260 rows=10000.00 loops=1)
                                                                    Buffers: shared hit=213
                                                              ->  Hash  (cost=0.00..0.00 rows=1 width=20) (actual time=0.004..0.005 rows=0.00 loops=1)
                                                                    Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                                    ->  Seq Scan on release_corrections c_year_1  (cost=0.00..0.00 rows=1 width=20) (actual time=0.004..0.004 rows=0.00 loops=1)
                                                                          Filter: (field = 'year'::text)
                                                        ->  Hash  (cost=4480.86..4480.86 rows=10000 width=16) (actual time=59.484..59.487 rows=9750.00 loops=1)
                                                              Buckets: 16384  Batches: 1  Memory Usage: 586kB
                                                              Buffers: shared hit=2419
                                                              ->  HashAggregate  (cost=4380.86..4480.86 rows=10000 width=16) (actual time=56.912..57.854 rows=9750.00 loops=1)
                                                                    Group Key: visible_media.release_id
                                                                    Batches: 1  Memory Usage: 793kB
                                                                    Buffers: shared hit=2419
                                                                    ->  Hash Join  (cost=319.00..4143.41 rows=94977 width=16) (actual time=2.613..41.475 rows=95000.00 loops=1)
                                                                          Hash Cond: (visible_tracks.medium_id = visible_media.id)
                                                                          Buffers: shared hit=2419
                                                                          ->  Seq Scan on tracks visible_tracks  (cost=0.00..3575.00 rows=94977 width=16) (actual time=0.012..21.765 rows=95000.00 loops=1)
                                                                                Filter: (source_status = 'present'::text)
                                                                                Rows Removed by Filter: 5000
                                                                                Buffers: shared hit=2325
                                                                          ->  Hash  (cost=194.00..194.00 rows=10000 width=32) (actual time=2.519..2.519 rows=10000.00 loops=1)
                                                                                Buckets: 16384  Batches: 1  Memory Usage: 753kB
                                                                                Buffers: shared hit=94
                                                                                ->  Seq Scan on media visible_media  (cost=0.00..194.00 rows=10000 width=32) (actual time=0.008..0.805 rows=10000.00 loops=1)
                                                                                      Buffers: shared hit=94
                                                        SubPlan 12
                                                          ->  Index Scan using release_groups_pkey on release_groups default_group  (cost=0.28..8.30 rows=1 width=16) (actual time=0.002..0.002 rows=1.00 loops=9750)
                                                                Index Cond: (id = r_1.group_id)
                                                                Index Searches: 9750
                                                                Buffers: shared hit=29250
                          ->  Hash  (cost=391.82..391.82 rows=10000 width=230) (actual time=8.700..8.704 rows=10000.00 loops=1)
                                Buckets: 16384  Batches: 1  Memory Usage: 1027kB
                                Buffers: shared hit=213
                                ->  Hash Left Join  (cost=0.04..391.82 rows=10000 width=230) (actual time=0.056..6.062 rows=10000.00 loops=1)
                                      Hash Cond: (r.id = c_year.release_id)
                                      Buffers: shared hit=213
                                      ->  Hash Left Join  (cost=0.03..365.55 rows=10000 width=210) (actual time=0.041..4.549 rows=10000.00 loops=1)
                                            Hash Cond: (r.id = c_album_artist.release_id)
                                            Buffers: shared hit=213
                                            ->  Hash Left Join  (cost=0.01..339.27 rows=10000 width=178) (actual time=0.028..2.998 rows=10000.00 loops=1)
                                                  Hash Cond: (r.id = c_title.release_id)
                                                  Buffers: shared hit=213
                                                  ->  Seq Scan on releases r  (cost=0.00..313.00 rows=10000 width=146) (actual time=0.014..1.678 rows=10000.00 loops=1)
                                                        Buffers: shared hit=213
                                                  ->  Hash  (cost=0.00..0.00 rows=1 width=48) (actual time=0.004..0.005 rows=0.00 loops=1)
                                                        Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                        ->  Seq Scan on release_corrections c_title  (cost=0.00..0.00 rows=1 width=48) (actual time=0.004..0.004 rows=0.00 loops=1)
                                                              Filter: (field = 'title'::text)
                                            ->  Hash  (cost=0.00..0.00 rows=1 width=48) (actual time=0.002..0.003 rows=0.00 loops=1)
                                                  Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                  ->  Seq Scan on release_corrections c_album_artist  (cost=0.00..0.00 rows=1 width=48) (actual time=0.002..0.002 rows=0.00 loops=1)
                                                        Filter: (field = 'album_artist'::text)
                                      ->  Hash  (cost=0.00..0.00 rows=1 width=20) (actual time=0.002..0.003 rows=0.00 loops=1)
                                            Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                            ->  Seq Scan on release_corrections c_year  (cost=0.00..0.00 rows=1 width=20) (actual time=0.002..0.002 rows=0.00 loops=1)
                                                  Filter: (field = 'year'::text)
                    ->  Seq Scan on release_artworks release_artwork  (cost=0.00..0.00 rows=1 width=88) (actual time=0.000..0.000 rows=0.00 loops=7800)
        SubPlan 1
          ->  Aggregate  (cost=51.27..51.28 rows=1 width=8) (actual time=0.035..0.035 rows=1.00 loops=50)
                Buffers: shared hit=800
                ->  Sort  (cost=51.22..51.25 rows=9 width=16) (actual time=0.032..0.033 rows=9.74 loops=50)
                      Sort Key: present_medium.id
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=800
                      ->  Nested Loop  (cost=4.78..51.08 rows=9 width=16) (actual time=0.023..0.029 rows=9.74 loops=50)
                            Buffers: shared hit=800
                            ->  Index Scan using media_release_position_uq on media present_medium  (cost=0.29..8.30 rows=1 width=16) (actual time=0.003..0.004 rows=1.00 loops=50)
                                  Index Cond: (release_id = r.id)
                                  Index Searches: 50
                                  Buffers: shared hit=150
                            ->  Bitmap Heap Scan on tracks present_track  (cost=4.50..42.68 rows=10 width=16) (actual time=0.013..0.018 rows=9.74 loops=50)
                                  Recheck Cond: (present_medium.id = medium_id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Heap Blocks: exact=500
                                  Buffers: shared hit=650
                                  ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.010..0.010 rows=10.00 loops=50)
                                        Index Cond: (medium_id = present_medium.id)
                                        Index Searches: 50
                                        Buffers: shared hit=150
        SubPlan 2
          ->  Aggregate  (cost=51.10..51.11 rows=1 width=8) (actual time=0.024..0.024 rows=1.00 loops=50)
                Buffers: shared hit=800
                ->  Nested Loop  (cost=4.78..51.08 rows=9 width=0) (actual time=0.017..0.023 rows=9.74 loops=50)
                      Buffers: shared hit=800
                      ->  Index Scan using media_release_position_uq on media present_medium_1  (cost=0.29..8.30 rows=1 width=16) (actual time=0.002..0.002 rows=1.00 loops=50)
                            Index Cond: (release_id = r.id)
                            Index Searches: 50
                            Buffers: shared hit=150
                      ->  Bitmap Heap Scan on tracks present_track_1  (cost=4.50..42.68 rows=10 width=16) (actual time=0.010..0.015 rows=9.74 loops=50)
                            Recheck Cond: (present_medium_1.id = medium_id)
                            Filter: (source_status = 'present'::text)
                            Rows Removed by Filter: 0
                            Heap Blocks: exact=500
                            Buffers: shared hit=650
                            ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.007..0.007 rows=10.00 loops=50)
                                  Index Cond: (medium_id = present_medium_1.id)
                                  Index Searches: 50
                                  Buffers: shared hit=150
        SubPlan 3
          ->  Aggregate  (cost=0.05..0.06 rows=1 width=8) (actual time=0.006..0.006 rows=1.00 loops=50)
                Buffers: shared hit=3
                ->  Sort  (cost=0.04..0.05 rows=1 width=32) (actual time=0.005..0.005 rows=0.00 loops=50)
                      Sort Key: decision.field_key
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=3
                      ->  Nested Loop Anti Join  (cost=0.00..0.03 rows=1 width=32) (actual time=0.002..0.002 rows=0.00 loops=50)
                            Join Filter: ((review.issue = decision.field_key) AND (review.basis = jsonb_build_object('reason', decision.reason, 'value', decision.selected_value, 'candidates', decision.candidates)))
                            ->  Nested Loop Anti Join  (cost=0.00..0.01 rows=1 width=144) (actual time=0.001..0.001 rows=0.00 loops=50)
                                  Join Filter: (correction.field = CASE decision.field_key WHEN 'artist'::text THEN 'album_artist'::text ELSE decision.field_key END)
                                  ->  Seq Scan on release_field_decisions decision  (cost=0.00..0.00 rows=1 width=144) (actual time=0.000..0.000 rows=0.00 loops=50)
                                        Filter: ((release_id = r.id) AND (action = 'uncertain_apply'::text))
                                  ->  Seq Scan on release_corrections correction  (cost=0.00..0.00 rows=1 width=48) (never executed)
                                        Filter: (release_id = r.id)
                            ->  Seq Scan on release_issue_reviews review  (cost=0.00..0.00 rows=1 width=80) (never executed)
                                  Filter: (release_id = r.id)
        SubPlan 5
          ->  Nested Loop Anti Join  (cost=0.00..0.02 rows=1 width=16) (actual time=0.003..0.004 rows=0.00 loops=1)
                Join Filter: ((review_1.release_id = "grouping".release_id) AND (review_1.basis = jsonb_build_object('reason', "grouping".reason, 'source_refs', "grouping".source_refs)))
                ->  Seq Scan on release_grouping_evidence "grouping"  (cost=0.00..0.00 rows=1 width=80) (actual time=0.003..0.003 rows=0.00 loops=1)
                      Filter: (NULLIF(btrim(reason), ''::text) IS NOT NULL)
                ->  Seq Scan on release_issue_reviews review_1  (cost=0.00..0.00 rows=1 width=48) (never executed)
                      Filter: (issue = 'grouping'::text)
        SubPlan 6
          ->  Aggregate  (cost=51.05..51.06 rows=1 width=8) (actual time=0.025..0.025 rows=1.00 loops=50)
                Buffers: shared hit=800
                ->  Sort  (cost=51.03..51.04 rows=1 width=69) (actual time=0.024..0.024 rows=0.26 loops=50)
                      Sort Key: (COALESCE(NULLIF(missing_track.source_identity, ''::text), (((missing_track.source_root_id)::text || ':'::text) || missing_track.relative_path)))
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=800
                      ->  Nested Loop Anti Join  (cost=4.78..51.02 rows=1 width=69) (actual time=0.021..0.023 rows=0.26 loops=50)
                            Join Filter: (((review_2.basis -> 'tracks'::text) -> (missing_track.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track.observed_at) * '1000000'::numeric))::bigint))
                            Buffers: shared hit=800
                            ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.021..0.022 rows=0.26 loops=50)
                                  Buffers: shared hit=800
                                  ->  Index Scan using media_release_position_uq on media missing_medium  (cost=0.29..8.30 rows=1 width=32) (actual time=0.002..0.002 rows=1.00 loops=50)
                                        Index Cond: (release_id = r.id)
                                        Index Searches: 50
                                        Buffers: shared hit=150
                                  ->  Bitmap Heap Scan on tracks missing_track  (cost=4.49..42.68 rows=1 width=109) (actual time=0.012..0.014 rows=0.26 loops=50)
                                        Recheck Cond: (missing_medium.id = medium_id)
                                        Filter: (source_status = 'missing'::text)
                                        Rows Removed by Filter: 10
                                        Heap Blocks: exact=500
                                        Buffers: shared hit=650
                                        ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.007..0.007 rows=10.00 loops=50)
                                              Index Cond: (medium_id = missing_medium.id)
                                              Index Searches: 50
                                              Buffers: shared hit=150
                            ->  Seq Scan on release_issue_reviews review_2  (cost=0.00..0.00 rows=1 width=48) (actual time=0.000..0.000 rows=0.00 loops=13)
                                  Filter: ((release_id = r.id) AND (issue = 'missing_tracks'::text))
        SubPlan 11
          ->  Aggregate  (cost=65.65..65.66 rows=1 width=8) (actual time=0.059..0.060 rows=1.00 loops=50)
                Buffers: shared hit=1264
                ->  Nested Loop Semi Join  (cost=0.99..14.50 rows=1 width=16) (actual time=0.009..0.009 rows=1.00 loops=50)
                      Buffers: shared hit=464
                      ->  Index Only Scan using releases_group_created_id_index on releases r_2  (cost=0.29..4.30 rows=1 width=16) (actual time=0.003..0.003 rows=1.00 loops=50)
                            Index Cond: (group_id = ranked.group_id)
                            Heap Fetches: 0
                            Index Searches: 50
                            Buffers: shared hit=101
                      ->  Nested Loop  (cost=0.70..10.18 rows=9 width=16) (actual time=0.005..0.005 rows=1.00 loops=50)
                            Buffers: shared hit=363
                            ->  Index Scan using media_release_position_uq on media visible_media_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.001..0.001 rows=1.00 loops=50)
                                  Index Cond: (release_id = r_2.id)
                                  Index Searches: 50
                                  Buffers: shared hit=150
                            ->  Index Scan using tracks_release_index on tracks visible_tracks_1  (cost=0.42..1.78 rows=10 width=16) (actual time=0.003..0.003 rows=1.00 loops=50)
                                  Index Cond: (medium_id = visible_media_1.id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Index Searches: 50
                                  Buffers: shared hit=213
                SubPlan 7
                  ->  Aggregate  (cost=0.05..0.06 rows=1 width=8) (actual time=0.003..0.003 rows=1.00 loops=50)
                        ->  Sort  (cost=0.04..0.05 rows=1 width=32) (actual time=0.003..0.003 rows=0.00 loops=50)
                              Sort Key: decision_1.field_key
                              Sort Method: quicksort  Memory: 25kB
                              ->  Nested Loop Anti Join  (cost=0.00..0.03 rows=1 width=32) (actual time=0.001..0.001 rows=0.00 loops=50)
                                    Join Filter: ((review_3.issue = decision_1.field_key) AND (review_3.basis = jsonb_build_object('reason', decision_1.reason, 'value', decision_1.selected_value, 'candidates', decision_1.candidates)))
                                    ->  Nested Loop Anti Join  (cost=0.00..0.01 rows=1 width=144) (actual time=0.001..0.001 rows=0.00 loops=50)
                                          Join Filter: (correction_1.field = CASE decision_1.field_key WHEN 'artist'::text THEN 'album_artist'::text ELSE decision_1.field_key END)
                                          ->  Seq Scan on release_field_decisions decision_1  (cost=0.00..0.00 rows=1 width=144) (actual time=0.000..0.000 rows=0.00 loops=50)
                                                Filter: ((release_id = r_2.id) AND (action = 'uncertain_apply'::text))
                                          ->  Seq Scan on release_corrections correction_1  (cost=0.00..0.00 rows=1 width=48) (never executed)
                                                Filter: (release_id = r_2.id)
                                    ->  Seq Scan on release_issue_reviews review_3  (cost=0.00..0.00 rows=1 width=80) (never executed)
                                          Filter: (release_id = r_2.id)
                SubPlan 8
                  ->  Nested Loop Anti Join  (cost=0.00..0.01 rows=1 width=0) (actual time=0.001..0.001 rows=0.00 loops=50)
                        Join Filter: (review_4.basis = jsonb_build_object('reason', grouping_1.reason, 'source_refs', grouping_1.source_refs))
                        ->  Seq Scan on release_grouping_evidence grouping_1  (cost=0.00..0.00 rows=1 width=80) (actual time=0.000..0.000 rows=0.00 loops=50)
                              Filter: ((release_id = r_2.id) AND (NULLIF(btrim(reason), ''::text) IS NOT NULL))
                        ->  Seq Scan on release_issue_reviews review_4  (cost=0.00..0.00 rows=1 width=48) (never executed)
                              Filter: ((release_id = r_2.id) AND (issue = 'grouping'::text))
                SubPlan 10
                  ->  Aggregate  (cost=51.05..51.06 rows=1 width=8) (actual time=0.043..0.043 rows=1.00 loops=50)
                        Buffers: shared hit=800
                        ->  Sort  (cost=51.03..51.04 rows=1 width=69) (actual time=0.033..0.033 rows=0.26 loops=50)
                              Sort Key: (COALESCE(NULLIF(missing_track_1.source_identity, ''::text), (((missing_track_1.source_root_id)::text || ':'::text) || missing_track_1.relative_path)))
                              Sort Method: quicksort  Memory: 25kB
                              Buffers: shared hit=800
                              ->  Nested Loop Anti Join  (cost=4.78..51.02 rows=1 width=69) (actual time=0.023..0.025 rows=0.26 loops=50)
                                    Join Filter: (((review_5.basis -> 'tracks'::text) -> (missing_track_1.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track_1.observed_at) * '1000000'::numeric))::bigint))
                                    Buffers: shared hit=800
                                    ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.023..0.024 rows=0.26 loops=50)
                                          Buffers: shared hit=800
                                          ->  Index Scan using media_release_position_uq on media missing_medium_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.003..0.003 rows=1.00 loops=50)
                                                Index Cond: (release_id = r_2.id)
                                                Index Searches: 50
                                                Buffers: shared hit=150
                                          ->  Bitmap Heap Scan on tracks missing_track_1  (cost=4.49..42.68 rows=1 width=109) (actual time=0.013..0.015 rows=0.26 loops=50)
                                                Recheck Cond: (missing_medium_1.id = medium_id)
                                                Filter: (source_status = 'missing'::text)
                                                Rows Removed by Filter: 10
                                                Heap Blocks: exact=500
                                                Buffers: shared hit=650
                                                ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.008..0.008 rows=10.00 loops=50)
                                                      Index Cond: (medium_id = missing_medium_1.id)
                                                      Index Searches: 50
                                                      Buffers: shared hit=150
                                    ->  Seq Scan on release_issue_reviews review_5  (cost=0.00..0.00 rows=1 width=48) (actual time=0.000..0.000 rows=0.00 loops=13)
                                          Filter: ((release_id = r_2.id) AND (issue = 'missing_tracks'::text))
Planning:
  Buffers: shared hit=979
Planning Time: 10.704 ms
Execution Time: 142.459 ms
```

所有事务结束后同一物理连接 SHOW jit 仍为 `on`。
