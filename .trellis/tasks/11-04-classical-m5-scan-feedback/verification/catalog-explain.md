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
Limit  (cost=256489.91..259059.40 rows=8 width=364) (actual time=111.140..112.610 rows=8.00 loops=1)
  Buffers: shared hit=32749
  ->  Result  (cost=256489.91..272549.25 rows=50 width=364) (actual time=111.138..112.606 rows=8.00 loops=1)
        Buffers: shared hit=32749
        ->  Sort  (cost=256489.91..256490.03 rows=50 width=348) (actual time=110.645..110.664 rows=8.00 loops=1)
              Sort Key: ranked.added_at DESC, ranked.group_id DESC
              Sort Method: top-N heapsort  Memory: 28kB
              Buffers: shared hit=32099
              ->  Nested Loop Left Join  (cost=89398.97..256488.91 rows=50 width=348) (actual time=87.783..107.971 rows=7800.00 loops=1)
                    Join Filter: (release_artwork.release_id = r.id)
                    Buffers: shared hit=32099
                    ->  Hash Join  (cost=89398.97..256149.66 rows=50 width=262) (actual time=87.759..103.947 rows=7800.00 loops=1)
                          Hash Cond: (ranked.id = r.id)
                          Buffers: shared hit=32099
                          ->  Subquery Scan on ranked  (cost=88836.83..255586.83 rows=50 width=48) (actual time=79.384..92.864 rows=7800.00 loops=1)
                                Filter: (ranked.member_rank = 1)
                                Buffers: shared hit=31883
                                ->  WindowAgg  (cost=88836.83..255461.83 rows=10000 width=70) (actual time=79.382..92.087 rows=7800.00 loops=1)
                                      Window: w2 AS (PARTITION BY r_1.group_id)
                                      Filter: ((row_number() OVER w1) <= 1)
                                      Rows Removed by Filter: 1950
                                      Storage: Memory  Maximum Storage: 17kB
                                      Buffers: shared hit=31883
                                      ->  WindowAgg  (cost=88836.83..172211.83 rows=10000 width=54) (actual time=79.369..84.939 rows=9750.00 loops=1)
                                            Window: w1 AS (PARTITION BY r_1.group_id ORDER BY ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id ROWS UNBOUNDED PRECEDING)
                                            Run Condition: (row_number() OVER w1 <= 1)
                                            Storage: Memory  Maximum Storage: 17kB
                                            Buffers: shared hit=31883
                                            ->  Sort  (cost=88836.83..88861.83 rows=10000 width=46) (actual time=79.351..80.515 rows=9750.00 loops=1)
                                                  Sort Key: r_1.group_id, ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id
                                                  Sort Method: quicksort  Memory: 994kB
                                                  Buffers: shared hit=31883
                                                  ->  Hash Join  (cost=4620.68..88172.44 rows=10000 width=46) (actual time=54.428..75.930 rows=9750.00 loops=1)
                                                        Hash Cond: (r_1.id = visible_media.release_id)
                                                        Buffers: shared hit=31883
                                                        ->  Hash Left Join  (cost=15.12..354.38 rows=10000 width=64) (actual time=0.034..3.609 rows=10000.00 loops=1)
                                                              Hash Cond: (r_1.id = c_year_1.release_id)
                                                              Buffers: shared hit=214
                                                              ->  Seq Scan on releases r_1  (cost=0.00..313.00 rows=10000 width=44) (actual time=0.008..2.149 rows=10000.00 loops=1)
                                                                    Buffers: shared hit=213
                                                              ->  Hash  (cost=15.08..15.08 rows=3 width=20) (actual time=0.009..0.010 rows=0.00 loops=1)
                                                                    Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                                    Buffers: shared hit=1
                                                                    ->  Bitmap Heap Scan on release_corrections c_year_1  (cost=7.97..15.08 rows=3 width=20) (actual time=0.008..0.009 rows=0.00 loops=1)
                                                                          Recheck Cond: (field = 'year'::text)
                                                                          Buffers: shared hit=1
                                                                          ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.004..0.004 rows=0.00 loops=1)
                                                                                Index Cond: (field = 'year'::text)
                                                                                Index Searches: 1
                                                                                Buffers: shared hit=1
                                                        ->  Hash  (cost=4480.57..4480.57 rows=10000 width=16) (actual time=54.275..54.278 rows=9750.00 loops=1)
                                                              Buckets: 16384  Batches: 1  Memory Usage: 586kB
                                                              Buffers: shared hit=2419
                                                              ->  HashAggregate  (cost=4380.57..4480.57 rows=10000 width=16) (actual time=52.122..52.875 rows=9750.00 loops=1)
                                                                    Group Key: visible_media.release_id
                                                                    Batches: 1  Memory Usage: 793kB
                                                                    Buffers: shared hit=2419
                                                                    ->  Hash Join  (cost=319.00..4143.27 rows=94920 width=16) (actual time=2.718..37.703 rows=95000.00 loops=1)
                                                                          Hash Cond: (visible_tracks.medium_id = visible_media.id)
                                                                          Buffers: shared hit=2419
                                                                          ->  Seq Scan on tracks visible_tracks  (cost=0.00..3575.00 rows=94920 width=16) (actual time=0.010..19.591 rows=95000.00 loops=1)
                                                                                Filter: (source_status = 'present'::text)
                                                                                Rows Removed by Filter: 5000
                                                                                Buffers: shared hit=2325
                                                                          ->  Hash  (cost=194.00..194.00 rows=10000 width=32) (actual time=2.643..2.643 rows=10000.00 loops=1)
                                                                                Buckets: 16384  Batches: 1  Memory Usage: 753kB
                                                                                Buffers: shared hit=94
                                                                                ->  Seq Scan on media visible_media  (cost=0.00..194.00 rows=10000 width=32) (actual time=0.006..0.946 rows=10000.00 loops=1)
                                                                                      Buffers: shared hit=94
                                                        SubPlan 12
                                                          ->  Index Scan using release_groups_pkey on release_groups default_group  (cost=0.28..8.30 rows=1 width=16) (actual time=0.001..0.001 rows=1.00 loops=9750)
                                                                Index Cond: (id = r_1.group_id)
                                                                Index Searches: 9750
                                                                Buffers: shared hit=29250
                          ->  Hash  (cost=437.14..437.14 rows=10000 width=230) (actual time=8.288..8.296 rows=10000.00 loops=1)
                                Buckets: 16384  Batches: 1  Memory Usage: 1027kB
                                Buffers: shared hit=216
                                ->  Hash Left Join  (cost=45.36..437.14 rows=10000 width=230) (actual time=0.086..5.862 rows=10000.00 loops=1)
                                      Hash Cond: (r.id = c_year.release_id)
                                      Buffers: shared hit=216
                                      ->  Hash Left Join  (cost=30.24..395.76 rows=10000 width=210) (actual time=0.058..4.373 rows=10000.00 loops=1)
                                            Hash Cond: (r.id = c_album_artist.release_id)
                                            Buffers: shared hit=215
                                            ->  Hash Left Join  (cost=15.12..354.38 rows=10000 width=178) (actual time=0.039..3.007 rows=10000.00 loops=1)
                                                  Hash Cond: (r.id = c_title.release_id)
                                                  Buffers: shared hit=214
                                                  ->  Seq Scan on releases r  (cost=0.00..313.00 rows=10000 width=146) (actual time=0.012..1.683 rows=10000.00 loops=1)
                                                        Buffers: shared hit=213
                                                  ->  Hash  (cost=15.08..15.08 rows=3 width=48) (actual time=0.009..0.010 rows=0.00 loops=1)
                                                        Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                        Buffers: shared hit=1
                                                        ->  Bitmap Heap Scan on release_corrections c_title  (cost=7.97..15.08 rows=3 width=48) (actual time=0.009..0.009 rows=0.00 loops=1)
                                                              Recheck Cond: (field = 'title'::text)
                                                              Buffers: shared hit=1
                                                              ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.004..0.004 rows=0.00 loops=1)
                                                                    Index Cond: (field = 'title'::text)
                                                                    Index Searches: 1
                                                                    Buffers: shared hit=1
                                            ->  Hash  (cost=15.08..15.08 rows=3 width=48) (actual time=0.004..0.005 rows=0.00 loops=1)
                                                  Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                  Buffers: shared hit=1
                                                  ->  Bitmap Heap Scan on release_corrections c_album_artist  (cost=7.97..15.08 rows=3 width=48) (actual time=0.004..0.004 rows=0.00 loops=1)
                                                        Recheck Cond: (field = 'album_artist'::text)
                                                        Buffers: shared hit=1
                                                        ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.001..0.002 rows=0.00 loops=1)
                                                              Index Cond: (field = 'album_artist'::text)
                                                              Index Searches: 1
                                                              Buffers: shared hit=1
                                      ->  Hash  (cost=15.08..15.08 rows=3 width=20) (actual time=0.009..0.010 rows=0.00 loops=1)
                                            Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                            Buffers: shared hit=1
                                            ->  Bitmap Heap Scan on release_corrections c_year  (cost=7.97..15.08 rows=3 width=20) (actual time=0.009..0.010 rows=0.00 loops=1)
                                                  Recheck Cond: (field = 'year'::text)
                                                  Buffers: shared hit=1
                                                  ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.001..0.001 rows=0.00 loops=1)
                                                        Index Cond: (field = 'year'::text)
                                                        Index Searches: 1
                                                        Buffers: shared hit=1
                    ->  Materialize  (cost=0.00..16.45 rows=430 width=88) (actual time=0.000..0.000 rows=0.00 loops=7800)
                          Storage: Memory  Maximum Storage: 17kB
                          ->  Seq Scan on release_artworks release_artwork  (cost=0.00..14.30 rows=430 width=88) (actual time=0.012..0.012 rows=0.00 loops=1)
        SubPlan 1
          ->  Aggregate  (cost=51.26..51.27 rows=1 width=8) (actual time=0.072..0.072 rows=1.00 loops=8)
                Buffers: shared hit=128
                ->  Sort  (cost=51.21..51.23 rows=9 width=16) (actual time=0.060..0.061 rows=9.75 loops=8)
                      Sort Key: present_medium.id
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=128
                      ->  Nested Loop  (cost=4.78..51.07 rows=9 width=16) (actual time=0.034..0.042 rows=9.75 loops=8)
                            Buffers: shared hit=128
                            ->  Index Scan using media_release_position_uq on media present_medium  (cost=0.29..8.30 rows=1 width=16) (actual time=0.007..0.008 rows=1.00 loops=8)
                                  Index Cond: (release_id = r.id)
                                  Index Searches: 8
                                  Buffers: shared hit=24
                            ->  Bitmap Heap Scan on tracks present_track  (cost=4.49..42.68 rows=9 width=16) (actual time=0.019..0.026 rows=9.75 loops=8)
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
          ->  Aggregate  (cost=51.09..51.10 rows=1 width=8) (actual time=0.034..0.034 rows=1.00 loops=8)
                Buffers: shared hit=128
                ->  Nested Loop  (cost=4.78..51.07 rows=9 width=0) (actual time=0.027..0.033 rows=9.75 loops=8)
                      Buffers: shared hit=128
                      ->  Index Scan using media_release_position_uq on media present_medium_1  (cost=0.29..8.30 rows=1 width=16) (actual time=0.003..0.003 rows=1.00 loops=8)
                            Index Cond: (release_id = r.id)
                            Index Searches: 8
                            Buffers: shared hit=24
                      ->  Bitmap Heap Scan on tracks present_track_1  (cost=4.49..42.68 rows=9 width=16) (actual time=0.016..0.020 rows=9.75 loops=8)
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
          ->  Hash Anti Join  (cost=16.18..37.69 rows=378 width=16) (actual time=0.005..0.005 rows=0.00 loops=1)
                Hash Cond: (("grouping".release_id = review_1.release_id) AND (jsonb_build_object('reason', "grouping".reason, 'source_refs', "grouping".source_refs) = review_1.basis))
                ->  Seq Scan on release_grouping_evidence "grouping"  (cost=0.00..15.70 rows=378 width=80) (actual time=0.004..0.005 rows=0.00 loops=1)
                      Filter: (NULLIF(btrim(reason), ''::text) IS NOT NULL)
                ->  Hash  (cost=16.13..16.13 rows=3 width=48) (never executed)
                      ->  Bitmap Heap Scan on release_issue_reviews review_1  (cost=9.03..16.13 rows=3 width=48) (never executed)
                            Recheck Cond: (issue = 'grouping'::text)
                            ->  Bitmap Index Scan on release_issue_reviews_pkey  (cost=0.00..9.03 rows=3 width=0) (never executed)
                                  Index Cond: (issue = 'grouping'::text)
                                  Index Searches: 0
        SubPlan 6
          ->  Aggregate  (cost=59.22..59.23 rows=1 width=8) (actual time=0.052..0.052 rows=1.00 loops=8)
                Buffers: shared hit=132
                ->  Sort  (cost=59.20..59.21 rows=1 width=69) (actual time=0.044..0.044 rows=0.25 loops=8)
                      Sort Key: (COALESCE(NULLIF(missing_track.source_identity, ''::text), (((missing_track.source_root_id)::text || ':'::text) || missing_track.relative_path)))
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=132
                      ->  Nested Loop Anti Join  (cost=4.93..59.19 rows=1 width=69) (actual time=0.034..0.035 rows=0.25 loops=8)
                            Join Filter: (((review_2.basis -> 'tracks'::text) -> (missing_track.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track.observed_at) * '1000000'::numeric))::bigint))
                            Buffers: shared hit=132
                            ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.030..0.031 rows=0.25 loops=8)
                                  Buffers: shared hit=128
                                  ->  Index Scan using media_release_position_uq on media missing_medium  (cost=0.29..8.30 rows=1 width=32) (actual time=0.003..0.003 rows=1.00 loops=8)
                                        Index Cond: (release_id = r.id)
                                        Index Searches: 8
                                        Buffers: shared hit=24
                                  ->  Bitmap Heap Scan on tracks missing_track  (cost=4.49..42.68 rows=1 width=109) (actual time=0.020..0.021 rows=0.25 loops=8)
                                        Recheck Cond: (missing_medium.id = medium_id)
                                        Filter: (source_status = 'missing'::text)
                                        Rows Removed by Filter: 10
                                        Heap Blocks: exact=80
                                        Buffers: shared hit=104
                                        ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.013..0.014 rows=10.00 loops=8)
                                              Index Cond: (medium_id = missing_medium.id)
                                              Index Searches: 8
                                              Buffers: shared hit=24
                            ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review_2  (cost=0.15..8.17 rows=1 width=48) (actual time=0.010..0.010 rows=0.00 loops=2)
                                  Index Cond: ((release_id = r.id) AND (issue = 'missing_tracks'::text))
                                  Index Searches: 2
                                  Buffers: shared hit=4
        SubPlan 11
          ->  Aggregate  (cost=118.63..118.64 rows=1 width=8) (actual time=0.071..0.071 rows=1.00 loops=8)
                Buffers: shared hit=246
                ->  Nested Loop Semi Join  (cost=0.99..18.49 rows=1 width=16) (actual time=0.012..0.013 rows=1.00 loops=8)
                      Buffers: shared hit=82
                      ->  Index Only Scan using releases_group_created_id_index on releases r_2  (cost=0.29..8.30 rows=1 width=16) (actual time=0.005..0.006 rows=1.00 loops=8)
                            Index Cond: (group_id = ranked.group_id)
                            Heap Fetches: 8
                            Index Searches: 8
                            Buffers: shared hit=24
                      ->  Nested Loop  (cost=0.70..10.17 rows=9 width=16) (actual time=0.006..0.006 rows=1.00 loops=8)
                            Buffers: shared hit=58
                            ->  Index Scan using media_release_position_uq on media visible_media_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.002..0.002 rows=1.00 loops=8)
                                  Index Cond: (release_id = r_2.id)
                                  Index Searches: 8
                                  Buffers: shared hit=24
                            ->  Index Scan using tracks_release_index on tracks visible_tracks_1  (cost=0.42..1.78 rows=9 width=16) (actual time=0.003..0.003 rows=1.00 loops=8)
                                  Index Cond: (medium_id = visible_media_1.id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Index Searches: 8
                                  Buffers: shared hit=34
                SubPlan 7
                  ->  Aggregate  (cost=24.53..24.54 rows=1 width=8) (actual time=0.003..0.003 rows=1.00 loops=8)
                        Buffers: shared hit=16
                        ->  Nested Loop Anti Join  (cost=0.44..24.53 rows=1 width=32) (actual time=0.002..0.002 rows=0.00 loops=8)
                              Buffers: shared hit=16
                              ->  Nested Loop Anti Join  (cost=0.29..16.35 rows=1 width=144) (actual time=0.002..0.002 rows=0.00 loops=8)
                                    Buffers: shared hit=16
                                    ->  Index Scan using release_field_decisions_release_id_field_key_key on release_field_decisions decision_1  (cost=0.14..8.16 rows=1 width=144) (actual time=0.001..0.002 rows=0.00 loops=8)
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
                  ->  Nested Loop Anti Join  (cost=0.30..16.36 rows=1 width=0) (actual time=0.004..0.004 rows=0.00 loops=8)
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
                  ->  Aggregate  (cost=59.22..59.23 rows=1 width=8) (actual time=0.048..0.048 rows=1.00 loops=8)
                        Buffers: shared hit=132
                        ->  Sort  (cost=59.20..59.21 rows=1 width=69) (actual time=0.040..0.040 rows=0.25 loops=8)
                              Sort Key: (COALESCE(NULLIF(missing_track_1.source_identity, ''::text), (((missing_track_1.source_root_id)::text || ':'::text) || missing_track_1.relative_path)))
                              Sort Method: quicksort  Memory: 25kB
                              Buffers: shared hit=132
                              ->  Nested Loop Anti Join  (cost=4.93..59.19 rows=1 width=69) (actual time=0.030..0.031 rows=0.25 loops=8)
                                    Join Filter: (((review_5.basis -> 'tracks'::text) -> (missing_track_1.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track_1.observed_at) * '1000000'::numeric))::bigint))
                                    Buffers: shared hit=132
                                    ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.028..0.029 rows=0.25 loops=8)
                                          Buffers: shared hit=128
                                          ->  Index Scan using media_release_position_uq on media missing_medium_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.004..0.004 rows=1.00 loops=8)
                                                Index Cond: (release_id = r_2.id)
                                                Index Searches: 8
                                                Buffers: shared hit=24
                                          ->  Bitmap Heap Scan on tracks missing_track_1  (cost=4.49..42.68 rows=1 width=109) (actual time=0.018..0.019 rows=0.25 loops=8)
                                                Recheck Cond: (missing_medium_1.id = medium_id)
                                                Filter: (source_status = 'missing'::text)
                                                Rows Removed by Filter: 10
                                                Heap Blocks: exact=80
                                                Buffers: shared hit=104
                                                ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.010..0.010 rows=10.00 loops=8)
                                                      Index Cond: (medium_id = missing_medium_1.id)
                                                      Index Searches: 8
                                                      Buffers: shared hit=24
                                    ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review_5  (cost=0.15..8.17 rows=1 width=48) (actual time=0.004..0.004 rows=0.00 loops=2)
                                          Index Cond: ((release_id = r_2.id) AND (issue = 'missing_tracks'::text))
                                          Index Searches: 2
                                          Buffers: shared hit=4
Planning:
  Buffers: shared hit=180
Planning Time: 5.140 ms
Execution Time: 114.279 ms
```

### 加入时间排序首页50项（含COUNT）

参数：`[]`；事务jit=off。

```text
Aggregate  (cost=172336.96..172336.97 rows=1 width=8) (actual time=86.054..86.060 rows=1.00 loops=1)
  Buffers: shared hit=31883
  ->  Subquery Scan on ranked  (cost=88836.83..172336.83 rows=50 width=0) (actual time=80.807..85.691 rows=7800.00 loops=1)
        Filter: (ranked.member_rank = 1)
        Buffers: shared hit=31883
        ->  WindowAgg  (cost=88836.83..172211.83 rows=10000 width=70) (actual time=80.805..85.000 rows=7800.00 loops=1)
              Window: w1 AS (PARTITION BY r.group_id ORDER BY ((r.id IS DISTINCT FROM (SubPlan 1))), ((NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0)), r.created_at, r.id ROWS UNBOUNDED PRECEDING)
              Run Condition: (row_number() OVER w1 <= 1)
              Storage: Memory  Maximum Storage: 17kB
              Buffers: shared hit=31883
              ->  Sort  (cost=88836.83..88861.83 rows=10000 width=46) (actual time=80.793..81.450 rows=9750.00 loops=1)
                    Sort Key: r.group_id, ((r.id IS DISTINCT FROM (SubPlan 1))), ((NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0)), r.created_at, r.id
                    Sort Method: quicksort  Memory: 994kB
                    Buffers: shared hit=31883
                    ->  Hash Join  (cost=4620.68..88172.44 rows=10000 width=46) (actual time=55.863..77.416 rows=9750.00 loops=1)
                          Hash Cond: (r.id = visible_media.release_id)
                          Buffers: shared hit=31883
                          ->  Hash Left Join  (cost=15.12..354.38 rows=10000 width=64) (actual time=0.022..3.567 rows=10000.00 loops=1)
                                Hash Cond: (r.id = c_year.release_id)
                                Buffers: shared hit=214
                                ->  Seq Scan on releases r  (cost=0.00..313.00 rows=10000 width=44) (actual time=0.011..2.121 rows=10000.00 loops=1)
                                      Buffers: shared hit=213
                                ->  Hash  (cost=15.08..15.08 rows=3 width=20) (actual time=0.007..0.008 rows=0.00 loops=1)
                                      Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                      Buffers: shared hit=1
                                      ->  Bitmap Heap Scan on release_corrections c_year  (cost=7.97..15.08 rows=3 width=20) (actual time=0.007..0.007 rows=0.00 loops=1)
                                            Recheck Cond: (field = 'year'::text)
                                            Buffers: shared hit=1
                                            ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.003..0.004 rows=0.00 loops=1)
                                                  Index Cond: (field = 'year'::text)
                                                  Index Searches: 1
                                                  Buffers: shared hit=1
                          ->  Hash  (cost=4480.57..4480.57 rows=10000 width=16) (actual time=55.740..55.743 rows=9750.00 loops=1)
                                Buckets: 16384  Batches: 1  Memory Usage: 586kB
                                Buffers: shared hit=2419
                                ->  HashAggregate  (cost=4380.57..4480.57 rows=10000 width=16) (actual time=53.411..54.224 rows=9750.00 loops=1)
                                      Group Key: visible_media.release_id
                                      Batches: 1  Memory Usage: 793kB
                                      Buffers: shared hit=2419
                                      ->  Hash Join  (cost=319.00..4143.27 rows=94920 width=16) (actual time=2.210..38.949 rows=95000.00 loops=1)
                                            Hash Cond: (visible_tracks.medium_id = visible_media.id)
                                            Buffers: shared hit=2419
                                            ->  Seq Scan on tracks visible_tracks  (cost=0.00..3575.00 rows=94920 width=16) (actual time=0.007..20.351 rows=95000.00 loops=1)
                                                  Filter: (source_status = 'present'::text)
                                                  Rows Removed by Filter: 5000
                                                  Buffers: shared hit=2325
                                            ->  Hash  (cost=194.00..194.00 rows=10000 width=32) (actual time=2.146..2.147 rows=10000.00 loops=1)
                                                  Buckets: 16384  Batches: 1  Memory Usage: 753kB
                                                  Buffers: shared hit=94
                                                  ->  Seq Scan on media visible_media  (cost=0.00..194.00 rows=10000 width=32) (actual time=0.006..0.845 rows=10000.00 loops=1)
                                                        Buffers: shared hit=94
                          SubPlan 1
                            ->  Index Scan using release_groups_pkey on release_groups default_group  (cost=0.28..8.30 rows=1 width=16) (actual time=0.001..0.001 rows=1.00 loops=9750)
                                  Index Cond: (id = r.group_id)
                                  Index Searches: 9750
                                  Buffers: shared hit=29250
Planning:
  Buffers: shared hit=37
Planning Time: 0.971 ms
Execution Time: 86.651 ms
```

参数：`[50 0]`；事务jit=off。

```text
Limit  (cost=256490.32..272549.66 rows=50 width=364) (actual time=109.596..116.008 rows=50.00 loops=1)
  Buffers: shared hit=36164
  ->  Result  (cost=256490.32..272549.66 rows=50 width=364) (actual time=109.595..115.996 rows=50.00 loops=1)
        Buffers: shared hit=36164
        ->  Sort  (cost=256490.32..256490.44 rows=50 width=348) (actual time=109.224..109.250 rows=50.00 loops=1)
              Sort Key: ranked.added_at DESC, ranked.group_id DESC
              Sort Method: top-N heapsort  Memory: 46kB
              Buffers: shared hit=32099
              ->  Nested Loop Left Join  (cost=89398.97..256488.91 rows=50 width=348) (actual time=88.232..106.709 rows=7800.00 loops=1)
                    Join Filter: (release_artwork.release_id = r.id)
                    Buffers: shared hit=32099
                    ->  Hash Join  (cost=89398.97..256149.66 rows=50 width=262) (actual time=88.208..102.862 rows=7800.00 loops=1)
                          Hash Cond: (ranked.id = r.id)
                          Buffers: shared hit=32099
                          ->  Subquery Scan on ranked  (cost=88836.83..255586.83 rows=50 width=48) (actual time=80.161..92.306 rows=7800.00 loops=1)
                                Filter: (ranked.member_rank = 1)
                                Buffers: shared hit=31883
                                ->  WindowAgg  (cost=88836.83..255461.83 rows=10000 width=70) (actual time=80.160..91.574 rows=7800.00 loops=1)
                                      Window: w2 AS (PARTITION BY r_1.group_id)
                                      Filter: ((row_number() OVER w1) <= 1)
                                      Rows Removed by Filter: 1950
                                      Storage: Memory  Maximum Storage: 17kB
                                      Buffers: shared hit=31883
                                      ->  WindowAgg  (cost=88836.83..172211.83 rows=10000 width=54) (actual time=80.142..85.029 rows=9750.00 loops=1)
                                            Window: w1 AS (PARTITION BY r_1.group_id ORDER BY ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id ROWS UNBOUNDED PRECEDING)
                                            Run Condition: (row_number() OVER w1 <= 1)
                                            Storage: Memory  Maximum Storage: 17kB
                                            Buffers: shared hit=31883
                                            ->  Sort  (cost=88836.83..88861.83 rows=10000 width=46) (actual time=80.127..80.887 rows=9750.00 loops=1)
                                                  Sort Key: r_1.group_id, ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id
                                                  Sort Method: quicksort  Memory: 994kB
                                                  Buffers: shared hit=31883
                                                  ->  Hash Join  (cost=4620.68..88172.44 rows=10000 width=46) (actual time=55.313..76.750 rows=9750.00 loops=1)
                                                        Hash Cond: (r_1.id = visible_media.release_id)
                                                        Buffers: shared hit=31883
                                                        ->  Hash Left Join  (cost=15.12..354.38 rows=10000 width=64) (actual time=0.026..3.316 rows=10000.00 loops=1)
                                                              Hash Cond: (r_1.id = c_year_1.release_id)
                                                              Buffers: shared hit=214
                                                              ->  Seq Scan on releases r_1  (cost=0.00..313.00 rows=10000 width=44) (actual time=0.007..1.860 rows=10000.00 loops=1)
                                                                    Buffers: shared hit=213
                                                              ->  Hash  (cost=15.08..15.08 rows=3 width=20) (actual time=0.008..0.010 rows=0.00 loops=1)
                                                                    Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                                    Buffers: shared hit=1
                                                                    ->  Bitmap Heap Scan on release_corrections c_year_1  (cost=7.97..15.08 rows=3 width=20) (actual time=0.008..0.009 rows=0.00 loops=1)
                                                                          Recheck Cond: (field = 'year'::text)
                                                                          Buffers: shared hit=1
                                                                          ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.004..0.004 rows=0.00 loops=1)
                                                                                Index Cond: (field = 'year'::text)
                                                                                Index Searches: 1
                                                                                Buffers: shared hit=1
                                                        ->  Hash  (cost=4480.57..4480.57 rows=10000 width=16) (actual time=55.171..55.174 rows=9750.00 loops=1)
                                                              Buckets: 16384  Batches: 1  Memory Usage: 586kB
                                                              Buffers: shared hit=2419
                                                              ->  HashAggregate  (cost=4380.57..4480.57 rows=10000 width=16) (actual time=52.785..53.610 rows=9750.00 loops=1)
                                                                    Group Key: visible_media.release_id
                                                                    Batches: 1  Memory Usage: 793kB
                                                                    Buffers: shared hit=2419
                                                                    ->  Hash Join  (cost=319.00..4143.27 rows=94920 width=16) (actual time=2.660..38.248 rows=95000.00 loops=1)
                                                                          Hash Cond: (visible_tracks.medium_id = visible_media.id)
                                                                          Buffers: shared hit=2419
                                                                          ->  Seq Scan on tracks visible_tracks  (cost=0.00..3575.00 rows=94920 width=16) (actual time=0.010..19.939 rows=95000.00 loops=1)
                                                                                Filter: (source_status = 'present'::text)
                                                                                Rows Removed by Filter: 5000
                                                                                Buffers: shared hit=2325
                                                                          ->  Hash  (cost=194.00..194.00 rows=10000 width=32) (actual time=2.583..2.584 rows=10000.00 loops=1)
                                                                                Buckets: 16384  Batches: 1  Memory Usage: 753kB
                                                                                Buffers: shared hit=94
                                                                                ->  Seq Scan on media visible_media  (cost=0.00..194.00 rows=10000 width=32) (actual time=0.006..0.909 rows=10000.00 loops=1)
                                                                                      Buffers: shared hit=94
                                                        SubPlan 12
                                                          ->  Index Scan using release_groups_pkey on release_groups default_group  (cost=0.28..8.30 rows=1 width=16) (actual time=0.001..0.001 rows=1.00 loops=9750)
                                                                Index Cond: (id = r_1.group_id)
                                                                Index Searches: 9750
                                                                Buffers: shared hit=29250
                          ->  Hash  (cost=437.14..437.14 rows=10000 width=230) (actual time=7.976..7.983 rows=10000.00 loops=1)
                                Buckets: 16384  Batches: 1  Memory Usage: 1027kB
                                Buffers: shared hit=216
                                ->  Hash Left Join  (cost=45.36..437.14 rows=10000 width=230) (actual time=0.067..5.769 rows=10000.00 loops=1)
                                      Hash Cond: (r.id = c_year.release_id)
                                      Buffers: shared hit=216
                                      ->  Hash Left Join  (cost=30.24..395.76 rows=10000 width=210) (actual time=0.048..4.334 rows=10000.00 loops=1)
                                            Hash Cond: (r.id = c_album_artist.release_id)
                                            Buffers: shared hit=215
                                            ->  Hash Left Join  (cost=15.12..354.38 rows=10000 width=178) (actual time=0.033..2.983 rows=10000.00 loops=1)
                                                  Hash Cond: (r.id = c_title.release_id)
                                                  Buffers: shared hit=214
                                                  ->  Seq Scan on releases r  (cost=0.00..313.00 rows=10000 width=146) (actual time=0.013..1.688 rows=10000.00 loops=1)
                                                        Buffers: shared hit=213
                                                  ->  Hash  (cost=15.08..15.08 rows=3 width=48) (actual time=0.009..0.010 rows=0.00 loops=1)
                                                        Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                        Buffers: shared hit=1
                                                        ->  Bitmap Heap Scan on release_corrections c_title  (cost=7.97..15.08 rows=3 width=48) (actual time=0.009..0.009 rows=0.00 loops=1)
                                                              Recheck Cond: (field = 'title'::text)
                                                              Buffers: shared hit=1
                                                              ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.004..0.005 rows=0.00 loops=1)
                                                                    Index Cond: (field = 'title'::text)
                                                                    Index Searches: 1
                                                                    Buffers: shared hit=1
                                            ->  Hash  (cost=15.08..15.08 rows=3 width=48) (actual time=0.004..0.005 rows=0.00 loops=1)
                                                  Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                  Buffers: shared hit=1
                                                  ->  Bitmap Heap Scan on release_corrections c_album_artist  (cost=7.97..15.08 rows=3 width=48) (actual time=0.004..0.004 rows=0.00 loops=1)
                                                        Recheck Cond: (field = 'album_artist'::text)
                                                        Buffers: shared hit=1
                                                        ->  Bitmap Index Scan on release_corrections_pkey  (cost=0.00..7.97 rows=3 width=0) (actual time=0.001..0.001 rows=0.00 loops=1)
                                                              Index Cond: (field = 'album_artist'::text)
                                                              Index Searches: 1
                                                              Buffers: shared hit=1
                                      ->  Hash  (cost=15.08..15.08 rows=3 width=20) (actual time=0.011..0.011 rows=0.00 loops=1)
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
                          ->  Seq Scan on release_artworks release_artwork  (cost=0.00..14.30 rows=430 width=88) (actual time=0.013..0.013 rows=0.00 loops=1)
        SubPlan 1
          ->  Aggregate  (cost=51.26..51.27 rows=1 width=8) (actual time=0.036..0.036 rows=1.00 loops=50)
                Buffers: shared hit=800
                ->  Sort  (cost=51.21..51.23 rows=9 width=16) (actual time=0.032..0.032 rows=9.74 loops=50)
                      Sort Key: present_medium.id
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=800
                      ->  Nested Loop  (cost=4.78..51.07 rows=9 width=16) (actual time=0.020..0.026 rows=9.74 loops=50)
                            Buffers: shared hit=800
                            ->  Index Scan using media_release_position_uq on media present_medium  (cost=0.29..8.30 rows=1 width=16) (actual time=0.003..0.003 rows=1.00 loops=50)
                                  Index Cond: (release_id = r.id)
                                  Index Searches: 50
                                  Buffers: shared hit=150
                            ->  Bitmap Heap Scan on tracks present_track  (cost=4.49..42.68 rows=9 width=16) (actual time=0.012..0.017 rows=9.74 loops=50)
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
          ->  Aggregate  (cost=51.09..51.10 rows=1 width=8) (actual time=0.024..0.024 rows=1.00 loops=50)
                Buffers: shared hit=800
                ->  Nested Loop  (cost=4.78..51.07 rows=9 width=0) (actual time=0.017..0.023 rows=9.74 loops=50)
                      Buffers: shared hit=800
                      ->  Index Scan using media_release_position_uq on media present_medium_1  (cost=0.29..8.30 rows=1 width=16) (actual time=0.002..0.002 rows=1.00 loops=50)
                            Index Cond: (release_id = r.id)
                            Index Searches: 50
                            Buffers: shared hit=150
                      ->  Bitmap Heap Scan on tracks present_track_1  (cost=4.49..42.68 rows=9 width=16) (actual time=0.010..0.015 rows=9.74 loops=50)
                            Recheck Cond: (present_medium_1.id = medium_id)
                            Filter: (source_status = 'present'::text)
                            Rows Removed by Filter: 0
                            Heap Blocks: exact=500
                            Buffers: shared hit=650
                            ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.008..0.008 rows=10.00 loops=50)
                                  Index Cond: (medium_id = present_medium_1.id)
                                  Index Searches: 50
                                  Buffers: shared hit=150
        SubPlan 3
          ->  Aggregate  (cost=24.53..24.54 rows=1 width=8) (actual time=0.002..0.002 rows=1.00 loops=50)
                Buffers: shared hit=100
                ->  Nested Loop Anti Join  (cost=0.44..24.53 rows=1 width=32) (actual time=0.002..0.002 rows=0.00 loops=50)
                      Buffers: shared hit=100
                      ->  Nested Loop Anti Join  (cost=0.29..16.35 rows=1 width=144) (actual time=0.001..0.001 rows=0.00 loops=50)
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
          ->  Hash Anti Join  (cost=16.18..37.69 rows=378 width=16) (actual time=0.004..0.006 rows=0.00 loops=1)
                Hash Cond: (("grouping".release_id = review_1.release_id) AND (jsonb_build_object('reason', "grouping".reason, 'source_refs', "grouping".source_refs) = review_1.basis))
                ->  Seq Scan on release_grouping_evidence "grouping"  (cost=0.00..15.70 rows=378 width=80) (actual time=0.004..0.004 rows=0.00 loops=1)
                      Filter: (NULLIF(btrim(reason), ''::text) IS NOT NULL)
                ->  Hash  (cost=16.13..16.13 rows=3 width=48) (never executed)
                      ->  Bitmap Heap Scan on release_issue_reviews review_1  (cost=9.03..16.13 rows=3 width=48) (never executed)
                            Recheck Cond: (issue = 'grouping'::text)
                            ->  Bitmap Index Scan on release_issue_reviews_pkey  (cost=0.00..9.03 rows=3 width=0) (never executed)
                                  Index Cond: (issue = 'grouping'::text)
                                  Index Searches: 0
        SubPlan 6
          ->  Aggregate  (cost=59.22..59.23 rows=1 width=8) (actual time=0.027..0.027 rows=1.00 loops=50)
                Buffers: shared hit=826
                ->  Sort  (cost=59.20..59.21 rows=1 width=69) (actual time=0.024..0.024 rows=0.26 loops=50)
                      Sort Key: (COALESCE(NULLIF(missing_track.source_identity, ''::text), (((missing_track.source_root_id)::text || ':'::text) || missing_track.relative_path)))
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=826
                      ->  Nested Loop Anti Join  (cost=4.93..59.19 rows=1 width=69) (actual time=0.019..0.021 rows=0.26 loops=50)
                            Join Filter: (((review_2.basis -> 'tracks'::text) -> (missing_track.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track.observed_at) * '1000000'::numeric))::bigint))
                            Buffers: shared hit=826
                            ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.018..0.020 rows=0.26 loops=50)
                                  Buffers: shared hit=800
                                  ->  Index Scan using media_release_position_uq on media missing_medium  (cost=0.29..8.30 rows=1 width=32) (actual time=0.002..0.002 rows=1.00 loops=50)
                                        Index Cond: (release_id = r.id)
                                        Index Searches: 50
                                        Buffers: shared hit=150
                                  ->  Bitmap Heap Scan on tracks missing_track  (cost=4.49..42.68 rows=1 width=109) (actual time=0.012..0.013 rows=0.26 loops=50)
                                        Recheck Cond: (missing_medium.id = medium_id)
                                        Filter: (source_status = 'missing'::text)
                                        Rows Removed by Filter: 10
                                        Heap Blocks: exact=500
                                        Buffers: shared hit=650
                                        ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.007..0.007 rows=10.00 loops=50)
                                              Index Cond: (medium_id = missing_medium.id)
                                              Index Searches: 50
                                              Buffers: shared hit=150
                            ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review_2  (cost=0.15..8.17 rows=1 width=48) (actual time=0.002..0.002 rows=0.00 loops=13)
                                  Index Cond: ((release_id = r.id) AND (issue = 'missing_tracks'::text))
                                  Index Searches: 13
                                  Buffers: shared hit=26
        SubPlan 11
          ->  Aggregate  (cost=118.63..118.64 rows=1 width=8) (actual time=0.041..0.041 rows=1.00 loops=50)
                Buffers: shared hit=1539
                ->  Nested Loop Semi Join  (cost=0.99..18.49 rows=1 width=16) (actual time=0.008..0.008 rows=1.00 loops=50)
                      Buffers: shared hit=513
                      ->  Index Only Scan using releases_group_created_id_index on releases r_2  (cost=0.29..8.30 rows=1 width=16) (actual time=0.003..0.003 rows=1.00 loops=50)
                            Index Cond: (group_id = ranked.group_id)
                            Heap Fetches: 50
                            Index Searches: 50
                            Buffers: shared hit=150
                      ->  Nested Loop  (cost=0.70..10.17 rows=9 width=16) (actual time=0.004..0.004 rows=1.00 loops=50)
                            Buffers: shared hit=363
                            ->  Index Scan using media_release_position_uq on media visible_media_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.001..0.001 rows=1.00 loops=50)
                                  Index Cond: (release_id = r_2.id)
                                  Index Searches: 50
                                  Buffers: shared hit=150
                            ->  Index Scan using tracks_release_index on tracks visible_tracks_1  (cost=0.42..1.78 rows=9 width=16) (actual time=0.002..0.002 rows=1.00 loops=50)
                                  Index Cond: (medium_id = visible_media_1.id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Index Searches: 50
                                  Buffers: shared hit=213
                SubPlan 7
                  ->  Aggregate  (cost=24.53..24.54 rows=1 width=8) (actual time=0.002..0.002 rows=1.00 loops=50)
                        Buffers: shared hit=100
                        ->  Nested Loop Anti Join  (cost=0.44..24.53 rows=1 width=32) (actual time=0.001..0.001 rows=0.00 loops=50)
                              Buffers: shared hit=100
                              ->  Nested Loop Anti Join  (cost=0.29..16.35 rows=1 width=144) (actual time=0.001..0.001 rows=0.00 loops=50)
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
                  ->  Aggregate  (cost=59.22..59.23 rows=1 width=8) (actual time=0.027..0.027 rows=1.00 loops=50)
                        Buffers: shared hit=826
                        ->  Sort  (cost=59.20..59.21 rows=1 width=69) (actual time=0.024..0.024 rows=0.26 loops=50)
                              Sort Key: (COALESCE(NULLIF(missing_track_1.source_identity, ''::text), (((missing_track_1.source_root_id)::text || ':'::text) || missing_track_1.relative_path)))
                              Sort Method: quicksort  Memory: 25kB
                              Buffers: shared hit=826
                              ->  Nested Loop Anti Join  (cost=4.93..59.19 rows=1 width=69) (actual time=0.019..0.020 rows=0.26 loops=50)
                                    Join Filter: (((review_5.basis -> 'tracks'::text) -> (missing_track_1.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track_1.observed_at) * '1000000'::numeric))::bigint))
                                    Buffers: shared hit=826
                                    ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.018..0.020 rows=0.26 loops=50)
                                          Buffers: shared hit=800
                                          ->  Index Scan using media_release_position_uq on media missing_medium_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.002..0.002 rows=1.00 loops=50)
                                                Index Cond: (release_id = r_2.id)
                                                Index Searches: 50
                                                Buffers: shared hit=150
                                          ->  Bitmap Heap Scan on tracks missing_track_1  (cost=4.49..42.68 rows=1 width=109) (actual time=0.012..0.013 rows=0.26 loops=50)
                                                Recheck Cond: (missing_medium_1.id = medium_id)
                                                Filter: (source_status = 'missing'::text)
                                                Rows Removed by Filter: 10
                                                Heap Blocks: exact=500
                                                Buffers: shared hit=650
                                                ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.007..0.007 rows=10.00 loops=50)
                                                      Index Cond: (medium_id = missing_medium_1.id)
                                                      Index Searches: 50
                                                      Buffers: shared hit=150
                                    ->  Index Scan using release_issue_reviews_pkey on release_issue_reviews review_5  (cost=0.15..8.17 rows=1 width=48) (actual time=0.001..0.001 rows=0.00 loops=13)
                                          Index Cond: ((release_id = r_2.id) AND (issue = 'missing_tracks'::text))
                                          Index Searches: 13
                                          Buffers: shared hit=26
Planning:
  Buffers: shared hit=180
Planning Time: 4.865 ms
Execution Time: 117.572 ms
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
Limit  (cost=256092.23..257877.85 rows=8 width=364) (actual time=115.172..116.543 rows=8.00 loops=1)
  Buffers: shared hit=32689
  ->  Result  (cost=256092.23..267252.32 rows=50 width=364) (actual time=115.170..116.539 rows=8.00 loops=1)
        Buffers: shared hit=32689
        ->  Sort  (cost=256092.23..256092.36 rows=50 width=348) (actual time=114.683..114.701 rows=8.00 loops=1)
              Sort Key: ranked.added_at DESC, ranked.group_id DESC
              Sort Method: top-N heapsort  Memory: 28kB
              Buffers: shared hit=32095
              ->  Nested Loop Left Join  (cost=89338.55..256091.23 rows=50 width=348) (actual time=88.290..111.874 rows=7800.00 loops=1)
                    Join Filter: (release_artwork.release_id = r.id)
                    Buffers: shared hit=32095
                    ->  Hash Join  (cost=89338.55..256089.23 rows=50 width=262) (actual time=88.273..104.986 rows=7800.00 loops=1)
                          Hash Cond: (ranked.id = r.id)
                          Buffers: shared hit=32095
                          ->  Subquery Scan on ranked  (cost=88821.72..255571.72 rows=50 width=48) (actual time=80.151..93.971 rows=7800.00 loops=1)
                                Filter: (ranked.member_rank = 1)
                                Buffers: shared hit=31882
                                ->  WindowAgg  (cost=88821.72..255446.72 rows=10000 width=70) (actual time=80.149..93.126 rows=7800.00 loops=1)
                                      Window: w2 AS (PARTITION BY r_1.group_id)
                                      Filter: ((row_number() OVER w1) <= 1)
                                      Rows Removed by Filter: 1950
                                      Storage: Memory  Maximum Storage: 17kB
                                      Buffers: shared hit=31882
                                      ->  WindowAgg  (cost=88821.72..172196.72 rows=10000 width=54) (actual time=80.136..85.615 rows=9750.00 loops=1)
                                            Window: w1 AS (PARTITION BY r_1.group_id ORDER BY ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id ROWS UNBOUNDED PRECEDING)
                                            Run Condition: (row_number() OVER w1 <= 1)
                                            Storage: Memory  Maximum Storage: 17kB
                                            Buffers: shared hit=31882
                                            ->  Sort  (cost=88821.72..88846.72 rows=10000 width=46) (actual time=80.121..81.151 rows=9750.00 loops=1)
                                                  Sort Key: r_1.group_id, ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id
                                                  Sort Method: quicksort  Memory: 994kB
                                                  Buffers: shared hit=31882
                                                  ->  Hash Join  (cost=4605.58..88157.34 rows=10000 width=46) (actual time=55.470..76.723 rows=9750.00 loops=1)
                                                        Hash Cond: (r_1.id = visible_media.release_id)
                                                        Buffers: shared hit=31882
                                                        ->  Hash Left Join  (cost=0.01..339.27 rows=10000 width=64) (actual time=0.025..3.471 rows=10000.00 loops=1)
                                                              Hash Cond: (r_1.id = c_year_1.release_id)
                                                              Buffers: shared hit=213
                                                              ->  Seq Scan on releases r_1  (cost=0.00..313.00 rows=10000 width=44) (actual time=0.010..2.020 rows=10000.00 loops=1)
                                                                    Buffers: shared hit=213
                                                              ->  Hash  (cost=0.00..0.00 rows=1 width=20) (actual time=0.004..0.005 rows=0.00 loops=1)
                                                                    Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                                    ->  Seq Scan on release_corrections c_year_1  (cost=0.00..0.00 rows=1 width=20) (actual time=0.003..0.003 rows=0.00 loops=1)
                                                                          Filter: (field = 'year'::text)
                                                        ->  Hash  (cost=4480.57..4480.57 rows=10000 width=16) (actual time=55.309..55.313 rows=9750.00 loops=1)
                                                              Buckets: 16384  Batches: 1  Memory Usage: 586kB
                                                              Buffers: shared hit=2419
                                                              ->  HashAggregate  (cost=4380.57..4480.57 rows=10000 width=16) (actual time=52.911..53.777 rows=9750.00 loops=1)
                                                                    Group Key: visible_media.release_id
                                                                    Batches: 1  Memory Usage: 793kB
                                                                    Buffers: shared hit=2419
                                                                    ->  Hash Join  (cost=319.00..4143.27 rows=94920 width=16) (actual time=2.650..38.419 rows=95000.00 loops=1)
                                                                          Hash Cond: (visible_tracks.medium_id = visible_media.id)
                                                                          Buffers: shared hit=2419
                                                                          ->  Seq Scan on tracks visible_tracks  (cost=0.00..3575.00 rows=94920 width=16) (actual time=0.015..20.317 rows=95000.00 loops=1)
                                                                                Filter: (source_status = 'present'::text)
                                                                                Rows Removed by Filter: 5000
                                                                                Buffers: shared hit=2325
                                                                          ->  Hash  (cost=194.00..194.00 rows=10000 width=32) (actual time=2.561..2.561 rows=10000.00 loops=1)
                                                                                Buckets: 16384  Batches: 1  Memory Usage: 753kB
                                                                                Buffers: shared hit=94
                                                                                ->  Seq Scan on media visible_media  (cost=0.00..194.00 rows=10000 width=32) (actual time=0.007..0.896 rows=10000.00 loops=1)
                                                                                      Buffers: shared hit=94
                                                        SubPlan 12
                                                          ->  Index Scan using release_groups_pkey on release_groups default_group  (cost=0.28..8.30 rows=1 width=16) (actual time=0.001..0.001 rows=1.00 loops=9750)
                                                                Index Cond: (id = r_1.group_id)
                                                                Index Searches: 9750
                                                                Buffers: shared hit=29250
                          ->  Hash  (cost=391.82..391.82 rows=10000 width=230) (actual time=8.055..8.061 rows=10000.00 loops=1)
                                Buckets: 16384  Batches: 1  Memory Usage: 1027kB
                                Buffers: shared hit=213
                                ->  Hash Left Join  (cost=0.04..391.82 rows=10000 width=230) (actual time=0.061..5.851 rows=10000.00 loops=1)
                                      Hash Cond: (r.id = c_year.release_id)
                                      Buffers: shared hit=213
                                      ->  Hash Left Join  (cost=0.03..365.55 rows=10000 width=210) (actual time=0.037..4.374 rows=10000.00 loops=1)
                                            Hash Cond: (r.id = c_album_artist.release_id)
                                            Buffers: shared hit=213
                                            ->  Hash Left Join  (cost=0.01..339.27 rows=10000 width=178) (actual time=0.024..3.006 rows=10000.00 loops=1)
                                                  Hash Cond: (r.id = c_title.release_id)
                                                  Buffers: shared hit=213
                                                  ->  Seq Scan on releases r  (cost=0.00..313.00 rows=10000 width=146) (actual time=0.012..1.784 rows=10000.00 loops=1)
                                                        Buffers: shared hit=213
                                                  ->  Hash  (cost=0.00..0.00 rows=1 width=48) (actual time=0.003..0.004 rows=0.00 loops=1)
                                                        Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                        ->  Seq Scan on release_corrections c_title  (cost=0.00..0.00 rows=1 width=48) (actual time=0.002..0.003 rows=0.00 loops=1)
                                                              Filter: (field = 'title'::text)
                                            ->  Hash  (cost=0.00..0.00 rows=1 width=48) (actual time=0.001..0.002 rows=0.00 loops=1)
                                                  Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                  ->  Seq Scan on release_corrections c_album_artist  (cost=0.00..0.00 rows=1 width=48) (actual time=0.001..0.001 rows=0.00 loops=1)
                                                        Filter: (field = 'album_artist'::text)
                                      ->  Hash  (cost=0.00..0.00 rows=1 width=20) (actual time=0.002..0.003 rows=0.00 loops=1)
                                            Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                            ->  Seq Scan on release_corrections c_year  (cost=0.00..0.00 rows=1 width=20) (actual time=0.002..0.002 rows=0.00 loops=1)
                                                  Filter: (field = 'year'::text)
                    ->  Seq Scan on release_artworks release_artwork  (cost=0.00..0.00 rows=1 width=88) (actual time=0.000..0.000 rows=0.00 loops=7800)
        SubPlan 1
          ->  Aggregate  (cost=51.26..51.27 rows=1 width=8) (actual time=0.064..0.065 rows=1.00 loops=8)
                Buffers: shared hit=128
                ->  Sort  (cost=51.21..51.23 rows=9 width=16) (actual time=0.055..0.056 rows=9.75 loops=8)
                      Sort Key: present_medium.id
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=128
                      ->  Nested Loop  (cost=4.78..51.07 rows=9 width=16) (actual time=0.032..0.041 rows=9.75 loops=8)
                            Buffers: shared hit=128
                            ->  Index Scan using media_release_position_uq on media present_medium  (cost=0.29..8.30 rows=1 width=16) (actual time=0.007..0.008 rows=1.00 loops=8)
                                  Index Cond: (release_id = r.id)
                                  Index Searches: 8
                                  Buffers: shared hit=24
                            ->  Bitmap Heap Scan on tracks present_track  (cost=4.49..42.68 rows=9 width=16) (actual time=0.018..0.026 rows=9.75 loops=8)
                                  Recheck Cond: (present_medium.id = medium_id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Heap Blocks: exact=80
                                  Buffers: shared hit=104
                                  ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.013..0.013 rows=10.00 loops=8)
                                        Index Cond: (medium_id = present_medium.id)
                                        Index Searches: 8
                                        Buffers: shared hit=24
        SubPlan 2
          ->  Aggregate  (cost=51.09..51.10 rows=1 width=8) (actual time=0.029..0.029 rows=1.00 loops=8)
                Buffers: shared hit=128
                ->  Nested Loop  (cost=4.78..51.07 rows=9 width=0) (actual time=0.022..0.027 rows=9.75 loops=8)
                      Buffers: shared hit=128
                      ->  Index Scan using media_release_position_uq on media present_medium_1  (cost=0.29..8.30 rows=1 width=16) (actual time=0.003..0.003 rows=1.00 loops=8)
                            Index Cond: (release_id = r.id)
                            Index Searches: 8
                            Buffers: shared hit=24
                      ->  Bitmap Heap Scan on tracks present_track_1  (cost=4.49..42.68 rows=9 width=16) (actual time=0.012..0.017 rows=9.75 loops=8)
                            Recheck Cond: (present_medium_1.id = medium_id)
                            Filter: (source_status = 'present'::text)
                            Rows Removed by Filter: 0
                            Heap Blocks: exact=80
                            Buffers: shared hit=104
                            ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.009..0.009 rows=10.00 loops=8)
                                  Index Cond: (medium_id = present_medium_1.id)
                                  Index Searches: 8
                                  Buffers: shared hit=24
        SubPlan 3
          ->  Aggregate  (cost=0.05..0.06 rows=1 width=8) (actual time=0.017..0.017 rows=1.00 loops=8)
                ->  Sort  (cost=0.04..0.05 rows=1 width=32) (actual time=0.012..0.012 rows=0.00 loops=8)
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
          ->  Nested Loop Anti Join  (cost=0.00..0.02 rows=1 width=16) (actual time=0.004..0.004 rows=0.00 loops=1)
                Join Filter: ((review_1.release_id = "grouping".release_id) AND (review_1.basis = jsonb_build_object('reason', "grouping".reason, 'source_refs', "grouping".source_refs)))
                ->  Seq Scan on release_grouping_evidence "grouping"  (cost=0.00..0.00 rows=1 width=80) (actual time=0.004..0.004 rows=0.00 loops=1)
                      Filter: (NULLIF(btrim(reason), ''::text) IS NOT NULL)
                ->  Seq Scan on release_issue_reviews review_1  (cost=0.00..0.00 rows=1 width=48) (never executed)
                      Filter: (issue = 'grouping'::text)
        SubPlan 6
          ->  Aggregate  (cost=51.05..51.06 rows=1 width=8) (actual time=0.038..0.038 rows=1.00 loops=8)
                Buffers: shared hit=128
                ->  Sort  (cost=51.03..51.04 rows=1 width=69) (actual time=0.032..0.032 rows=0.25 loops=8)
                      Sort Key: (COALESCE(NULLIF(missing_track.source_identity, ''::text), (((missing_track.source_root_id)::text || ':'::text) || missing_track.relative_path)))
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=128
                      ->  Nested Loop Anti Join  (cost=4.78..51.02 rows=1 width=69) (actual time=0.024..0.025 rows=0.25 loops=8)
                            Join Filter: (((review_2.basis -> 'tracks'::text) -> (missing_track.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track.observed_at) * '1000000'::numeric))::bigint))
                            Buffers: shared hit=128
                            ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.023..0.024 rows=0.25 loops=8)
                                  Buffers: shared hit=128
                                  ->  Index Scan using media_release_position_uq on media missing_medium  (cost=0.29..8.30 rows=1 width=32) (actual time=0.003..0.003 rows=1.00 loops=8)
                                        Index Cond: (release_id = r.id)
                                        Index Searches: 8
                                        Buffers: shared hit=24
                                  ->  Bitmap Heap Scan on tracks missing_track  (cost=4.49..42.68 rows=1 width=109) (actual time=0.014..0.015 rows=0.25 loops=8)
                                        Recheck Cond: (missing_medium.id = medium_id)
                                        Filter: (source_status = 'missing'::text)
                                        Rows Removed by Filter: 10
                                        Heap Blocks: exact=80
                                        Buffers: shared hit=104
                                        ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.008..0.008 rows=10.00 loops=8)
                                              Index Cond: (medium_id = missing_medium.id)
                                              Index Searches: 8
                                              Buffers: shared hit=24
                            ->  Seq Scan on release_issue_reviews review_2  (cost=0.00..0.00 rows=1 width=48) (actual time=0.002..0.002 rows=0.00 loops=2)
                                  Filter: ((release_id = r.id) AND (issue = 'missing_tracks'::text))
        SubPlan 11
          ->  Aggregate  (cost=69.64..69.65 rows=1 width=8) (actual time=0.072..0.073 rows=1.00 loops=8)
                Buffers: shared hit=210
                ->  Nested Loop Semi Join  (cost=0.99..18.49 rows=1 width=16) (actual time=0.013..0.013 rows=1.00 loops=8)
                      Buffers: shared hit=82
                      ->  Index Only Scan using releases_group_created_id_index on releases r_2  (cost=0.29..8.30 rows=1 width=16) (actual time=0.005..0.006 rows=1.00 loops=8)
                            Index Cond: (group_id = ranked.group_id)
                            Heap Fetches: 8
                            Index Searches: 8
                            Buffers: shared hit=24
                      ->  Nested Loop  (cost=0.70..10.17 rows=9 width=16) (actual time=0.006..0.006 rows=1.00 loops=8)
                            Buffers: shared hit=58
                            ->  Index Scan using media_release_position_uq on media visible_media_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.002..0.002 rows=1.00 loops=8)
                                  Index Cond: (release_id = r_2.id)
                                  Index Searches: 8
                                  Buffers: shared hit=24
                            ->  Index Scan using tracks_release_index on tracks visible_tracks_1  (cost=0.42..1.78 rows=9 width=16) (actual time=0.003..0.003 rows=1.00 loops=8)
                                  Index Cond: (medium_id = visible_media_1.id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Index Searches: 8
                                  Buffers: shared hit=34
                SubPlan 7
                  ->  Aggregate  (cost=0.05..0.06 rows=1 width=8) (actual time=0.014..0.015 rows=1.00 loops=8)
                        ->  Sort  (cost=0.04..0.05 rows=1 width=32) (actual time=0.009..0.009 rows=0.00 loops=8)
                              Sort Key: decision_1.field_key
                              Sort Method: quicksort  Memory: 25kB
                              ->  Nested Loop Anti Join  (cost=0.00..0.03 rows=1 width=32) (actual time=0.002..0.002 rows=0.00 loops=8)
                                    Join Filter: ((review_3.issue = decision_1.field_key) AND (review_3.basis = jsonb_build_object('reason', decision_1.reason, 'value', decision_1.selected_value, 'candidates', decision_1.candidates)))
                                    ->  Nested Loop Anti Join  (cost=0.00..0.01 rows=1 width=144) (actual time=0.001..0.001 rows=0.00 loops=8)
                                          Join Filter: (correction_1.field = CASE decision_1.field_key WHEN 'artist'::text THEN 'album_artist'::text ELSE decision_1.field_key END)
                                          ->  Seq Scan on release_field_decisions decision_1  (cost=0.00..0.00 rows=1 width=144) (actual time=0.000..0.001 rows=0.00 loops=8)
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
                  ->  Aggregate  (cost=51.05..51.06 rows=1 width=8) (actual time=0.040..0.040 rows=1.00 loops=8)
                        Buffers: shared hit=128
                        ->  Sort  (cost=51.03..51.04 rows=1 width=69) (actual time=0.035..0.036 rows=0.25 loops=8)
                              Sort Key: (COALESCE(NULLIF(missing_track_1.source_identity, ''::text), (((missing_track_1.source_root_id)::text || ':'::text) || missing_track_1.relative_path)))
                              Sort Method: quicksort  Memory: 25kB
                              Buffers: shared hit=128
                              ->  Nested Loop Anti Join  (cost=4.78..51.02 rows=1 width=69) (actual time=0.028..0.029 rows=0.25 loops=8)
                                    Join Filter: (((review_5.basis -> 'tracks'::text) -> (missing_track_1.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track_1.observed_at) * '1000000'::numeric))::bigint))
                                    Buffers: shared hit=128
                                    ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.027..0.028 rows=0.25 loops=8)
                                          Buffers: shared hit=128
                                          ->  Index Scan using media_release_position_uq on media missing_medium_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.004..0.005 rows=1.00 loops=8)
                                                Index Cond: (release_id = r_2.id)
                                                Index Searches: 8
                                                Buffers: shared hit=24
                                          ->  Bitmap Heap Scan on tracks missing_track_1  (cost=4.49..42.68 rows=1 width=109) (actual time=0.017..0.018 rows=0.25 loops=8)
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
Planning Time: 4.591 ms
Execution Time: 117.874 ms
```

### 加入时间排序首页50项（含COUNT）

参数：`[]`；事务jit=off。

```text
Aggregate  (cost=172321.85..172321.86 rows=1 width=8) (actual time=90.473..90.481 rows=1.00 loops=1)
  Buffers: shared hit=31882
  ->  Subquery Scan on ranked  (cost=88821.72..172321.72 rows=50 width=0) (actual time=84.984..90.105 rows=7800.00 loops=1)
        Filter: (ranked.member_rank = 1)
        Buffers: shared hit=31882
        ->  WindowAgg  (cost=88821.72..172196.72 rows=10000 width=70) (actual time=84.983..89.407 rows=7800.00 loops=1)
              Window: w1 AS (PARTITION BY r.group_id ORDER BY ((r.id IS DISTINCT FROM (SubPlan 1))), ((NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0)), r.created_at, r.id ROWS UNBOUNDED PRECEDING)
              Run Condition: (row_number() OVER w1 <= 1)
              Storage: Memory  Maximum Storage: 17kB
              Buffers: shared hit=31882
              ->  Sort  (cost=88821.72..88846.72 rows=10000 width=46) (actual time=84.966..85.783 rows=9750.00 loops=1)
                    Sort Key: r.group_id, ((r.id IS DISTINCT FROM (SubPlan 1))), ((NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year.release_id IS NULL) THEN r.year ELSE c_year.year_value END, 0)), r.created_at, r.id
                    Sort Method: quicksort  Memory: 994kB
                    Buffers: shared hit=31882
                    ->  Hash Join  (cost=4605.58..88157.34 rows=10000 width=46) (actual time=58.234..81.387 rows=9750.00 loops=1)
                          Hash Cond: (r.id = visible_media.release_id)
                          Buffers: shared hit=31882
                          ->  Hash Left Join  (cost=0.01..339.27 rows=10000 width=64) (actual time=0.024..3.538 rows=10000.00 loops=1)
                                Hash Cond: (r.id = c_year.release_id)
                                Buffers: shared hit=213
                                ->  Seq Scan on releases r  (cost=0.00..313.00 rows=10000 width=44) (actual time=0.016..2.003 rows=10000.00 loops=1)
                                      Buffers: shared hit=213
                                ->  Hash  (cost=0.00..0.00 rows=1 width=20) (actual time=0.004..0.004 rows=0.00 loops=1)
                                      Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                      ->  Seq Scan on release_corrections c_year  (cost=0.00..0.00 rows=1 width=20) (actual time=0.003..0.003 rows=0.00 loops=1)
                                            Filter: (field = 'year'::text)
                          ->  Hash  (cost=4480.57..4480.57 rows=10000 width=16) (actual time=58.080..58.082 rows=9750.00 loops=1)
                                Buckets: 16384  Batches: 1  Memory Usage: 586kB
                                Buffers: shared hit=2419
                                ->  HashAggregate  (cost=4380.57..4480.57 rows=10000 width=16) (actual time=55.646..56.516 rows=9750.00 loops=1)
                                      Group Key: visible_media.release_id
                                      Batches: 1  Memory Usage: 793kB
                                      Buffers: shared hit=2419
                                      ->  Hash Join  (cost=319.00..4143.27 rows=94920 width=16) (actual time=2.991..40.199 rows=95000.00 loops=1)
                                            Hash Cond: (visible_tracks.medium_id = visible_media.id)
                                            Buffers: shared hit=2419
                                            ->  Seq Scan on tracks visible_tracks  (cost=0.00..3575.00 rows=94920 width=16) (actual time=0.007..20.275 rows=95000.00 loops=1)
                                                  Filter: (source_status = 'present'::text)
                                                  Rows Removed by Filter: 5000
                                                  Buffers: shared hit=2325
                                            ->  Hash  (cost=194.00..194.00 rows=10000 width=32) (actual time=2.909..2.910 rows=10000.00 loops=1)
                                                  Buckets: 16384  Batches: 1  Memory Usage: 753kB
                                                  Buffers: shared hit=94
                                                  ->  Seq Scan on media visible_media  (cost=0.00..194.00 rows=10000 width=32) (actual time=0.007..1.342 rows=10000.00 loops=1)
                                                        Buffers: shared hit=94
                          SubPlan 1
                            ->  Index Scan using release_groups_pkey on release_groups default_group  (cost=0.28..8.30 rows=1 width=16) (actual time=0.001..0.001 rows=1.00 loops=9750)
                                  Index Cond: (id = r.group_id)
                                  Index Searches: 9750
                                  Buffers: shared hit=29250
Planning:
  Buffers: shared hit=37
Planning Time: 1.195 ms
Execution Time: 91.161 ms
```

参数：`[50 0]`；事务jit=off。

```text
Limit  (cost=256092.64..267252.73 rows=50 width=364) (actual time=119.371..127.069 rows=50.00 loops=1)
  Buffers: shared hit=35808
  ->  Result  (cost=256092.64..267252.73 rows=50 width=364) (actual time=119.369..127.054 rows=50.00 loops=1)
        Buffers: shared hit=35808
        ->  Sort  (cost=256092.64..256092.77 rows=50 width=348) (actual time=118.860..118.889 rows=50.00 loops=1)
              Sort Key: ranked.added_at DESC, ranked.group_id DESC
              Sort Method: top-N heapsort  Memory: 46kB
              Buffers: shared hit=32095
              ->  Nested Loop Left Join  (cost=89338.55..256091.23 rows=50 width=348) (actual time=92.251..116.015 rows=7800.00 loops=1)
                    Join Filter: (release_artwork.release_id = r.id)
                    Buffers: shared hit=32095
                    ->  Hash Join  (cost=89338.55..256089.23 rows=50 width=262) (actual time=92.234..108.995 rows=7800.00 loops=1)
                          Hash Cond: (ranked.id = r.id)
                          Buffers: shared hit=32095
                          ->  Subquery Scan on ranked  (cost=88821.72..255571.72 rows=50 width=48) (actual time=83.306..97.289 rows=7800.00 loops=1)
                                Filter: (ranked.member_rank = 1)
                                Buffers: shared hit=31882
                                ->  WindowAgg  (cost=88821.72..255446.72 rows=10000 width=70) (actual time=83.304..96.454 rows=7800.00 loops=1)
                                      Window: w2 AS (PARTITION BY r_1.group_id)
                                      Filter: ((row_number() OVER w1) <= 1)
                                      Rows Removed by Filter: 1950
                                      Storage: Memory  Maximum Storage: 17kB
                                      Buffers: shared hit=31882
                                      ->  WindowAgg  (cost=88821.72..172196.72 rows=10000 width=54) (actual time=83.279..88.792 rows=9750.00 loops=1)
                                            Window: w1 AS (PARTITION BY r_1.group_id ORDER BY ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id ROWS UNBOUNDED PRECEDING)
                                            Run Condition: (row_number() OVER w1 <= 1)
                                            Storage: Memory  Maximum Storage: 17kB
                                            Buffers: shared hit=31882
                                            ->  Sort  (cost=88821.72..88846.72 rows=10000 width=46) (actual time=83.263..84.288 rows=9750.00 loops=1)
                                                  Sort Key: r_1.group_id, ((r_1.id IS DISTINCT FROM (SubPlan 12))), ((NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0) IS NULL)), (NULLIF(CASE WHEN (c_year_1.release_id IS NULL) THEN r_1.year ELSE c_year_1.year_value END, 0)), r_1.created_at, r_1.id
                                                  Sort Method: quicksort  Memory: 994kB
                                                  Buffers: shared hit=31882
                                                  ->  Hash Join  (cost=4605.58..88157.34 rows=10000 width=46) (actual time=56.865..79.626 rows=9750.00 loops=1)
                                                        Hash Cond: (r_1.id = visible_media.release_id)
                                                        Buffers: shared hit=31882
                                                        ->  Hash Left Join  (cost=0.01..339.27 rows=10000 width=64) (actual time=0.047..3.528 rows=10000.00 loops=1)
                                                              Hash Cond: (r_1.id = c_year_1.release_id)
                                                              Buffers: shared hit=213
                                                              ->  Seq Scan on releases r_1  (cost=0.00..313.00 rows=10000 width=44) (actual time=0.027..1.976 rows=10000.00 loops=1)
                                                                    Buffers: shared hit=213
                                                              ->  Hash  (cost=0.00..0.00 rows=1 width=20) (actual time=0.004..0.005 rows=0.00 loops=1)
                                                                    Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                                    ->  Seq Scan on release_corrections c_year_1  (cost=0.00..0.00 rows=1 width=20) (actual time=0.004..0.004 rows=0.00 loops=1)
                                                                          Filter: (field = 'year'::text)
                                                        ->  Hash  (cost=4480.57..4480.57 rows=10000 width=16) (actual time=56.678..56.681 rows=9750.00 loops=1)
                                                              Buckets: 16384  Batches: 1  Memory Usage: 586kB
                                                              Buffers: shared hit=2419
                                                              ->  HashAggregate  (cost=4380.57..4480.57 rows=10000 width=16) (actual time=54.237..55.088 rows=9750.00 loops=1)
                                                                    Group Key: visible_media.release_id
                                                                    Batches: 1  Memory Usage: 793kB
                                                                    Buffers: shared hit=2419
                                                                    ->  Hash Join  (cost=319.00..4143.27 rows=94920 width=16) (actual time=2.743..39.173 rows=95000.00 loops=1)
                                                                          Hash Cond: (visible_tracks.medium_id = visible_media.id)
                                                                          Buffers: shared hit=2419
                                                                          ->  Seq Scan on tracks visible_tracks  (cost=0.00..3575.00 rows=94920 width=16) (actual time=0.015..20.176 rows=95000.00 loops=1)
                                                                                Filter: (source_status = 'present'::text)
                                                                                Rows Removed by Filter: 5000
                                                                                Buffers: shared hit=2325
                                                                          ->  Hash  (cost=194.00..194.00 rows=10000 width=32) (actual time=2.642..2.643 rows=10000.00 loops=1)
                                                                                Buckets: 16384  Batches: 1  Memory Usage: 753kB
                                                                                Buffers: shared hit=94
                                                                                ->  Seq Scan on media visible_media  (cost=0.00..194.00 rows=10000 width=32) (actual time=0.007..0.905 rows=10000.00 loops=1)
                                                                                      Buffers: shared hit=94
                                                        SubPlan 12
                                                          ->  Index Scan using release_groups_pkey on release_groups default_group  (cost=0.28..8.30 rows=1 width=16) (actual time=0.001..0.001 rows=1.00 loops=9750)
                                                                Index Cond: (id = r_1.group_id)
                                                                Index Searches: 9750
                                                                Buffers: shared hit=29250
                          ->  Hash  (cost=391.82..391.82 rows=10000 width=230) (actual time=8.853..8.858 rows=10000.00 loops=1)
                                Buckets: 16384  Batches: 1  Memory Usage: 1027kB
                                Buffers: shared hit=213
                                ->  Hash Left Join  (cost=0.04..391.82 rows=10000 width=230) (actual time=0.055..6.321 rows=10000.00 loops=1)
                                      Hash Cond: (r.id = c_year.release_id)
                                      Buffers: shared hit=213
                                      ->  Hash Left Join  (cost=0.03..365.55 rows=10000 width=210) (actual time=0.040..4.707 rows=10000.00 loops=1)
                                            Hash Cond: (r.id = c_album_artist.release_id)
                                            Buffers: shared hit=213
                                            ->  Hash Left Join  (cost=0.01..339.27 rows=10000 width=178) (actual time=0.026..3.179 rows=10000.00 loops=1)
                                                  Hash Cond: (r.id = c_title.release_id)
                                                  Buffers: shared hit=213
                                                  ->  Seq Scan on releases r  (cost=0.00..313.00 rows=10000 width=146) (actual time=0.012..1.872 rows=10000.00 loops=1)
                                                        Buffers: shared hit=213
                                                  ->  Hash  (cost=0.00..0.00 rows=1 width=48) (actual time=0.003..0.003 rows=0.00 loops=1)
                                                        Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                        ->  Seq Scan on release_corrections c_title  (cost=0.00..0.00 rows=1 width=48) (actual time=0.003..0.003 rows=0.00 loops=1)
                                                              Filter: (field = 'title'::text)
                                            ->  Hash  (cost=0.00..0.00 rows=1 width=48) (actual time=0.002..0.003 rows=0.00 loops=1)
                                                  Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                                  ->  Seq Scan on release_corrections c_album_artist  (cost=0.00..0.00 rows=1 width=48) (actual time=0.002..0.002 rows=0.00 loops=1)
                                                        Filter: (field = 'album_artist'::text)
                                      ->  Hash  (cost=0.00..0.00 rows=1 width=20) (actual time=0.001..0.002 rows=0.00 loops=1)
                                            Buckets: 1024  Batches: 1  Memory Usage: 8kB
                                            ->  Seq Scan on release_corrections c_year  (cost=0.00..0.00 rows=1 width=20) (actual time=0.001..0.001 rows=0.00 loops=1)
                                                  Filter: (field = 'year'::text)
                    ->  Seq Scan on release_artworks release_artwork  (cost=0.00..0.00 rows=1 width=88) (actual time=0.000..0.000 rows=0.00 loops=7800)
        SubPlan 1
          ->  Aggregate  (cost=51.26..51.27 rows=1 width=8) (actual time=0.037..0.037 rows=1.00 loops=50)
                Buffers: shared hit=800
                ->  Sort  (cost=51.21..51.23 rows=9 width=16) (actual time=0.034..0.035 rows=9.74 loops=50)
                      Sort Key: present_medium.id
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=800
                      ->  Nested Loop  (cost=4.78..51.07 rows=9 width=16) (actual time=0.024..0.031 rows=9.74 loops=50)
                            Buffers: shared hit=800
                            ->  Index Scan using media_release_position_uq on media present_medium  (cost=0.29..8.30 rows=1 width=16) (actual time=0.004..0.004 rows=1.00 loops=50)
                                  Index Cond: (release_id = r.id)
                                  Index Searches: 50
                                  Buffers: shared hit=150
                            ->  Bitmap Heap Scan on tracks present_track  (cost=4.49..42.68 rows=9 width=16) (actual time=0.014..0.019 rows=9.74 loops=50)
                                  Recheck Cond: (present_medium.id = medium_id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Heap Blocks: exact=500
                                  Buffers: shared hit=650
                                  ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.011..0.011 rows=10.00 loops=50)
                                        Index Cond: (medium_id = present_medium.id)
                                        Index Searches: 50
                                        Buffers: shared hit=150
        SubPlan 2
          ->  Aggregate  (cost=51.09..51.10 rows=1 width=8) (actual time=0.026..0.026 rows=1.00 loops=50)
                Buffers: shared hit=800
                ->  Nested Loop  (cost=4.78..51.07 rows=9 width=0) (actual time=0.018..0.024 rows=9.74 loops=50)
                      Buffers: shared hit=800
                      ->  Index Scan using media_release_position_uq on media present_medium_1  (cost=0.29..8.30 rows=1 width=16) (actual time=0.002..0.002 rows=1.00 loops=50)
                            Index Cond: (release_id = r.id)
                            Index Searches: 50
                            Buffers: shared hit=150
                      ->  Bitmap Heap Scan on tracks present_track_1  (cost=4.49..42.68 rows=9 width=16) (actual time=0.010..0.015 rows=9.74 loops=50)
                            Recheck Cond: (present_medium_1.id = medium_id)
                            Filter: (source_status = 'present'::text)
                            Rows Removed by Filter: 0
                            Heap Blocks: exact=500
                            Buffers: shared hit=650
                            ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.008..0.008 rows=10.00 loops=50)
                                  Index Cond: (medium_id = present_medium_1.id)
                                  Index Searches: 50
                                  Buffers: shared hit=150
        SubPlan 3
          ->  Aggregate  (cost=0.05..0.06 rows=1 width=8) (actual time=0.005..0.005 rows=1.00 loops=50)
                ->  Sort  (cost=0.04..0.05 rows=1 width=32) (actual time=0.004..0.004 rows=0.00 loops=50)
                      Sort Key: decision.field_key
                      Sort Method: quicksort  Memory: 25kB
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
          ->  Nested Loop Anti Join  (cost=0.00..0.02 rows=1 width=16) (actual time=0.004..0.004 rows=0.00 loops=1)
                Join Filter: ((review_1.release_id = "grouping".release_id) AND (review_1.basis = jsonb_build_object('reason', "grouping".reason, 'source_refs', "grouping".source_refs)))
                ->  Seq Scan on release_grouping_evidence "grouping"  (cost=0.00..0.00 rows=1 width=80) (actual time=0.004..0.004 rows=0.00 loops=1)
                      Filter: (NULLIF(btrim(reason), ''::text) IS NOT NULL)
                ->  Seq Scan on release_issue_reviews review_1  (cost=0.00..0.00 rows=1 width=48) (never executed)
                      Filter: (issue = 'grouping'::text)
        SubPlan 6
          ->  Aggregate  (cost=51.05..51.06 rows=1 width=8) (actual time=0.027..0.027 rows=1.00 loops=50)
                Buffers: shared hit=800
                ->  Sort  (cost=51.03..51.04 rows=1 width=69) (actual time=0.026..0.026 rows=0.26 loops=50)
                      Sort Key: (COALESCE(NULLIF(missing_track.source_identity, ''::text), (((missing_track.source_root_id)::text || ':'::text) || missing_track.relative_path)))
                      Sort Method: quicksort  Memory: 25kB
                      Buffers: shared hit=800
                      ->  Nested Loop Anti Join  (cost=4.78..51.02 rows=1 width=69) (actual time=0.023..0.024 rows=0.26 loops=50)
                            Join Filter: (((review_2.basis -> 'tracks'::text) -> (missing_track.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track.observed_at) * '1000000'::numeric))::bigint))
                            Buffers: shared hit=800
                            ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.022..0.023 rows=0.26 loops=50)
                                  Buffers: shared hit=800
                                  ->  Index Scan using media_release_position_uq on media missing_medium  (cost=0.29..8.30 rows=1 width=32) (actual time=0.002..0.002 rows=1.00 loops=50)
                                        Index Cond: (release_id = r.id)
                                        Index Searches: 50
                                        Buffers: shared hit=150
                                  ->  Bitmap Heap Scan on tracks missing_track  (cost=4.49..42.68 rows=1 width=109) (actual time=0.013..0.014 rows=0.26 loops=50)
                                        Recheck Cond: (missing_medium.id = medium_id)
                                        Filter: (source_status = 'missing'::text)
                                        Rows Removed by Filter: 10
                                        Heap Blocks: exact=500
                                        Buffers: shared hit=650
                                        ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.008..0.008 rows=10.00 loops=50)
                                              Index Cond: (medium_id = missing_medium.id)
                                              Index Searches: 50
                                              Buffers: shared hit=150
                            ->  Seq Scan on release_issue_reviews review_2  (cost=0.00..0.00 rows=1 width=48) (actual time=0.000..0.000 rows=0.00 loops=13)
                                  Filter: ((release_id = r.id) AND (issue = 'missing_tracks'::text))
        SubPlan 11
          ->  Aggregate  (cost=69.64..69.65 rows=1 width=8) (actual time=0.064..0.064 rows=1.00 loops=50)
                Buffers: shared hit=1313
                ->  Nested Loop Semi Join  (cost=0.99..18.49 rows=1 width=16) (actual time=0.009..0.009 rows=1.00 loops=50)
                      Buffers: shared hit=513
                      ->  Index Only Scan using releases_group_created_id_index on releases r_2  (cost=0.29..8.30 rows=1 width=16) (actual time=0.004..0.004 rows=1.00 loops=50)
                            Index Cond: (group_id = ranked.group_id)
                            Heap Fetches: 50
                            Index Searches: 50
                            Buffers: shared hit=150
                      ->  Nested Loop  (cost=0.70..10.17 rows=9 width=16) (actual time=0.005..0.005 rows=1.00 loops=50)
                            Buffers: shared hit=363
                            ->  Index Scan using media_release_position_uq on media visible_media_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.002..0.002 rows=1.00 loops=50)
                                  Index Cond: (release_id = r_2.id)
                                  Index Searches: 50
                                  Buffers: shared hit=150
                            ->  Index Scan using tracks_release_index on tracks visible_tracks_1  (cost=0.42..1.78 rows=9 width=16) (actual time=0.002..0.002 rows=1.00 loops=50)
                                  Index Cond: (medium_id = visible_media_1.id)
                                  Filter: (source_status = 'present'::text)
                                  Rows Removed by Filter: 0
                                  Index Searches: 50
                                  Buffers: shared hit=213
                SubPlan 7
                  ->  Aggregate  (cost=0.05..0.06 rows=1 width=8) (actual time=0.005..0.005 rows=1.00 loops=50)
                        ->  Sort  (cost=0.04..0.05 rows=1 width=32) (actual time=0.004..0.004 rows=0.00 loops=50)
                              Sort Key: decision_1.field_key
                              Sort Method: quicksort  Memory: 25kB
                              ->  Nested Loop Anti Join  (cost=0.00..0.03 rows=1 width=32) (actual time=0.002..0.002 rows=0.00 loops=50)
                                    Join Filter: ((review_3.issue = decision_1.field_key) AND (review_3.basis = jsonb_build_object('reason', decision_1.reason, 'value', decision_1.selected_value, 'candidates', decision_1.candidates)))
                                    ->  Nested Loop Anti Join  (cost=0.00..0.01 rows=1 width=144) (actual time=0.001..0.001 rows=0.00 loops=50)
                                          Join Filter: (correction_1.field = CASE decision_1.field_key WHEN 'artist'::text THEN 'album_artist'::text ELSE decision_1.field_key END)
                                          ->  Seq Scan on release_field_decisions decision_1  (cost=0.00..0.00 rows=1 width=144) (actual time=0.001..0.001 rows=0.00 loops=50)
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
                  ->  Aggregate  (cost=51.05..51.06 rows=1 width=8) (actual time=0.045..0.045 rows=1.00 loops=50)
                        Buffers: shared hit=800
                        ->  Sort  (cost=51.03..51.04 rows=1 width=69) (actual time=0.035..0.036 rows=0.26 loops=50)
                              Sort Key: (COALESCE(NULLIF(missing_track_1.source_identity, ''::text), (((missing_track_1.source_root_id)::text || ':'::text) || missing_track_1.relative_path)))
                              Sort Method: quicksort  Memory: 25kB
                              Buffers: shared hit=800
                              ->  Nested Loop Anti Join  (cost=4.78..51.02 rows=1 width=69) (actual time=0.023..0.025 rows=0.26 loops=50)
                                    Join Filter: (((review_5.basis -> 'tracks'::text) -> (missing_track_1.id)::text) = to_jsonb(((EXTRACT(epoch FROM missing_track_1.observed_at) * '1000000'::numeric))::bigint))
                                    Buffers: shared hit=800
                                    ->  Nested Loop  (cost=4.78..50.99 rows=1 width=109) (actual time=0.022..0.024 rows=0.26 loops=50)
                                          Buffers: shared hit=800
                                          ->  Index Scan using media_release_position_uq on media missing_medium_1  (cost=0.29..8.30 rows=1 width=32) (actual time=0.003..0.003 rows=1.00 loops=50)
                                                Index Cond: (release_id = r_2.id)
                                                Index Searches: 50
                                                Buffers: shared hit=150
                                          ->  Bitmap Heap Scan on tracks missing_track_1  (cost=4.49..42.68 rows=1 width=109) (actual time=0.014..0.015 rows=0.26 loops=50)
                                                Recheck Cond: (missing_medium_1.id = medium_id)
                                                Filter: (source_status = 'missing'::text)
                                                Rows Removed by Filter: 10
                                                Heap Blocks: exact=500
                                                Buffers: shared hit=650
                                                ->  Bitmap Index Scan on tracks_release_index  (cost=0.00..4.49 rows=10 width=0) (actual time=0.007..0.007 rows=10.00 loops=50)
                                                      Index Cond: (medium_id = missing_medium_1.id)
                                                      Index Searches: 50
                                                      Buffers: shared hit=150
                                    ->  Seq Scan on release_issue_reviews review_5  (cost=0.00..0.00 rows=1 width=48) (actual time=0.000..0.000 rows=0.00 loops=13)
                                          Filter: ((release_id = r_2.id) AND (issue = 'missing_tracks'::text))
Planning:
  Buffers: shared hit=180
Planning Time: 4.753 ms
Execution Time: 128.581 ms
```

所有事务结束后同一物理连接 SHOW jit 仍为 `on`。

## 完整 HTTP 结果与结论（2026-11-04）

生产改动仅为两个延迟敏感读取入口增加短只读 REPEATABLE READ 事务，并在开始后执行 `SET LOCAL jit = off`。未改查询形状、索引、连接池默认值、数据库全局配置或 `enable_*` 开关；ANALYZE 仅发生在测试准备阶段。配置失败按 `catalog.ErrQuery` → HTTP 503 `database_unavailable` 处理，不忽略错误后继续读取。

验收采用 **正常构建、真实 TCP HTTP 服务、真实普通成员鉴权**。服务端计时覆盖完整应用 handler（会话查询、事务、所有 SQL、JSON 序列化、响应写出及 Flush），不包括 TCP 建连和请求解析；客户端完整收包另记。每状态/端点先记首读，再热身5次、正式采样40次；nearest-rank p95 为排序后第38项，不删除异常样本。首读不等同冷盘，未清缓存。规模固定100,000曲目/10,000发行/8,000组/5%缺失；两个阶段使用同一数据，辅助五表均空。

| 辅助表状态 | recent p50 / p95 / max（ms） | added_at_desc 首页50项 p50 / p95 / max（ms） | 门槛 |
| --- | --- | --- | --- |
| 空表、未 ANALYZE | 93.366 / **100.959** / 120.502 | 168.339 / **194.115** / 207.214 | ≤150 / ≤300 ms，均通过 |
| 空表、ANALYZE 后 | 97.383 / **104.354** / 121.825 | 177.512 / **229.816** / 235.229 | ≤150 / ≤300 ms，均通过 |

首读服务端时间依次为未统计 recent 130.172ms / 排序187.329ms、已统计 recent103.696ms / 排序177.370ms。统计前后均核验 `pg_class` 与 `pg_stat_all_tables`：辅助表 `reltuples=-1 → 0`，自动统计次数0，未统计时分析次数0、已统计时1，避免误把已自动统计的空表当作未统计。

完整方法、环境、每次请求原始样本、统计状态证据见 [JIT-only 验收基线](./catalog-http-performance-jit-only.md)；[采样历史](./catalog-http-performance.md) 保留早期不同缺失分布与 race 诊断轮次，不混用于本表。采样期间发生过短暂本地编译/vet，已如实记录，未剔除受影响样本；这是当前机器顺序请求基线，不是多用户并发或冷盘 SLA。race 规模测试只验证正确性，不用于性能验收。

### EXPLAIN 解释

本文件上方为同等规模、同样混合缺失分布的正常构建实测：

| 辅助表状态 | recent SQL 执行 | 排序 COUNT 执行 | 排序列表执行 | 排序两条 SQL 合计（不含 planning） |
| --- | ---: | ---: | ---: | ---: |
| 未 ANALYZE | 114.279ms | 86.651ms | 117.572ms | 204.223ms |
| 已 ANALYZE | 117.874ms | 91.161ms | 128.581ms | 219.742ms |

- 六条计划均不再出现 JIT 编译段；同一物理连接事务内 `SHOW jit=off`，事务外仍为原值 `on`。专门集成测试同时覆盖成功提交、读取失败回滚、配置失败短路以及两个公开入口结束后配置不泄漏。
- 所有排序在内存中完成（top-N/quick sort），哈希节点单批，无临时文件读写；仍需扫描可见成员并聚合，不能把 recent 上限8理解成数据库只读取8行。
- EXPLAIN 包含插桩开销、为独立 fixture 的单次计划，不等同完整 HTTP p95。排序端点的 COUNT 不能从成本中漏掉。
- 相比前轮2千组计划中约194/169ms的 JIT 编译，去除该固定开销后，即使增至8千组/10万曲目，两种辅助表统计状态均达到完整 HTTP 门槛。因此本轮不再扩大 SQL 改写或新增索引；若数据量或并发负载继续扩大，应使用同一 harness 重新验收。
