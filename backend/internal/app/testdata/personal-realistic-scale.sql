-- 独立的合成目录夹具，不依赖搜索夹具，不写入修正、依据、核对或封面辅助表。
-- 轨道身份按发行/轨号排列，收藏与歌单则按轨号/发行交错，避免一批只投影少数发行。
INSERT INTO release_groups(id)
SELECT ('30000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid
FROM generate_series(1,8000) i;

INSERT INTO releases(id,group_id,title,artist,album_artist,year,source_root_id,relative_directory)
SELECT ('20000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 ('30000000-0000-4000-8000-'||lpad((((i-1)%8000)+1)::text,12,'0'))::uuid,
 '合成发行 '||i,'合成艺人','合成艺人',1990+(i-1)/8000,
 '70000000-0000-4000-8000-000000000001'::uuid,'synthetic/'||i
FROM generate_series(1,10000) i;

INSERT INTO media(id,release_id,position,title)
SELECT ('40000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 ('20000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,1,'合成唱片'
FROM generate_series(1,10000) i;

INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,
 source_status,observed_at,source_kind,duration_seconds,codec,bit_depth,sample_rate,channels)
SELECT ('10000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 ('40000000-0000-4000-8000-'||lpad((((i-1)/10)+1)::text,12,'0'))::uuid,
 (i-1)%10+1,'合成曲目 '||i,'合成艺人',1,'70000000-0000-4000-8000-000000000001'::uuid,
 'synthetic/'||((i-1)/10+1)||'/'||((i-1)%10+1)||'.flac',
 CASE WHEN (((i-1)/10+1)+((i-1)%10+1))%20=0 THEN 'missing' ELSE 'present' END,
 '2026-11-04T00:00:00Z'::timestamptz,'physical',120,'flac',24,96000,2
FROM generate_series(1,100000) i;

INSERT INTO personal_favorite_state(user_id,revision) VALUES($1::uuid,1),($2::uuid,1);

INSERT INTO favorite_tracks(user_id,track_id,created_at)
SELECT $1::uuid,('10000000-0000-4000-8000-'||lpad(((i%10000)*10+i/10000+1)::text,12,'0'))::uuid,
 '2026-11-04T00:00:00Z'::timestamptz-i*interval '1 microsecond'
FROM generate_series(0,19999) i
UNION ALL
SELECT $2::uuid,id,'2026-11-04T00:00:00Z'::timestamptz FROM tracks;

INSERT INTO favorite_albums(user_id,group_id,created_at,saved_title,saved_artist)
SELECT $1::uuid,('30000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 '2026-11-04T00:00:00Z'::timestamptz-i*interval '1 microsecond','合成发行 '||i,'合成艺人'
FROM generate_series(1,1000) i
UNION ALL
SELECT $2::uuid,id,'2026-11-04T00:00:00Z'::timestamptz,'其他用户的收藏','合成艺人' FROM release_groups;

INSERT INTO personal_playlists(id,user_id,name,track_count,created_at,updated_at)
SELECT ('50000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 CASE WHEN i<=20 THEN $1::uuid ELSE $2::uuid END,
 CASE WHEN i<=20 THEN '合成歌单 '||i ELSE '其他用户歌单 '||i END,
 CASE WHEN i<=20 THEN 10000 ELSE 0 END,
 '2026-11-04T00:00:00Z'::timestamptz,
 '2026-11-04T00:00:00Z'::timestamptz-i*interval '1 second'
FROM generate_series(1,40) i;

-- 20 个长度 10000、步长 5000 的环形窗口：全库每轨恰好出现两次，相邻歌单重叠 5000 轨。
INSERT INTO personal_playlist_items(id,user_id,playlist_id,track_id,order_key,added_at)
SELECT ('60000000-0000-4000-8000-'||lpad(((p-1)*10000+j)::text,12,'0'))::uuid,$1::uuid,
 ('50000000-0000-4000-8000-'||lpad(p::text,12,'0'))::uuid,
 ('10000000-0000-4000-8000-'||lpad(((s%10000)*10+s/10000+1)::text,12,'0'))::uuid,
 j::bigint*4294967296,'2026-11-04T00:00:00Z'::timestamptz
FROM generate_series(1,20) p CROSS JOIN generate_series(1,10000) j
CROSS JOIN LATERAL (SELECT ((p-1)*5000+j-1)%100000 AS s) sequence;
