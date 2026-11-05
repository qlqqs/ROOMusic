-- 仅在隔离 schema 使用：十万 present、一万 missing、一万发行、八千组。
-- 预置两万共享身份与四千不同 key 别名的初始路由，不预造署名、profiles 或搜索投影。
-- sha256(bytea) 是 PostgreSQL 内置函数，不依赖 pgcrypto 或新增扩展。
INSERT INTO artists(id,initial_name)
SELECT ('60000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,'Artist '||lpad(i::text,5,'0') FROM generate_series(1,20000) i;
INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id)
SELECT ('61000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,1,'artist '||lpad(i::text,5,'0'),
 sha256(convert_to('artist '||lpad(i::text,5,'0'),'UTF8')),('60000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid FROM generate_series(1,20000) i;
INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id)
SELECT ('62000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,1,'alias '||lpad(i::text,5,'0'),
 sha256(convert_to('alias '||lpad(i::text,5,'0'),'UTF8')),('60000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid FROM generate_series(1,4000) i;
INSERT INTO artist_names(id,key_id,spelling,spelling_hash,origin)
SELECT ('63000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,('61000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 'Artist '||lpad(i::text,5,'0'),sha256(convert_to('Artist '||lpad(i::text,5,'0'),'UTF8')),'seed' FROM generate_series(1,20000) i;
INSERT INTO artist_names(id,key_id,spelling,spelling_hash,origin)
SELECT ('64000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,('62000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 'Alias '||lpad(i::text,5,'0'),sha256(convert_to('Alias '||lpad(i::text,5,'0'),'UTF8')),'seed' FROM generate_series(1,4000) i;
-- 合成的大规模别名扇出用例，非真实人物关系：Teresa Teng 指向已有热门种子身份。
INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id)
VALUES('62000000-0000-4000-8000-000000020001',1,'teresa teng',sha256(convert_to('teresa teng','UTF8')),'a0000000-0000-4000-8000-000000000001');
INSERT INTO artist_names(id,key_id,spelling,spelling_hash,origin)
VALUES('64000000-0000-4000-8000-000000020001','62000000-0000-4000-8000-000000020001','Teresa Teng',sha256(convert_to('Teresa Teng','UTF8')),'seed');

INSERT INTO library_roots(id,path,status,revision) VALUES('10000000-0000-4000-8000-000000000001','/synthetic-scale','disabled',1);
INSERT INTO release_groups(id) SELECT ('20000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid FROM generate_series(1,8000) i;
INSERT INTO releases(id,group_id,title,artist,album_artist,source_root_id,relative_directory,candidate_kind,label,catalog_number,year)
SELECT ('30000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 ('20000000-0000-4000-8000-'||lpad((CASE WHEN i<=8000 THEN i ELSE i-8000 END)::text,12,'0'))::uuid,
 '合成专辑 '||lpad(i::text,5,'0')||CASE WHEN i=8001 THEN ' 非代表命中' WHEN i=9901 THEN ' missingonlyneedle' ELSE '' END,
 CASE WHEN i<=5000 THEN '周杰倫' ELSE 'Artist '||lpad(i::text,5,'0') END,
 CASE WHEN i<=5000 THEN '周杰倫' ELSE 'Artist '||lpad(i::text,5,'0') END,
 '10000000-0000-4000-8000-000000000001','release-'||i,'ordinary_directory',
 CASE WHEN i<=5000 THEN '热门厂牌' ELSE '厂牌 '||i END,'SRCL-'||i,1980+i%40
FROM generate_series(1,10000) i;
INSERT INTO media(id,release_id,position,title)
SELECT ('40000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 ('30000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,1,'CD 1' FROM generate_series(1,10000) i;
INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,codec,duration_seconds,bit_depth,sample_rate,channels)
SELECT ('50000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 ('40000000-0000-4000-8000-'||lpad((CASE WHEN i<=50000 THEN 1+(i-1)%5000 WHEN i<=100000 THEN 5001+(i-50001)%4900 ELSE 9901+(i-100001)%100 END)::text,12,'0'))::uuid,
 i,CASE WHEN i=10001 THEN '包含 交响曲 第00999号 末尾' WHEN i=10002 THEN '交响曲 第00999号 前缀' ELSE
 CASE WHEN i%97=0 THEN '爱 ' ELSE '' END || '交响曲 第'||lpad(i::text,greatest(5,length(i::text)),'0')||'号' || CASE WHEN i%101=0 THEN ' %_~' ELSE '' END END ||
 CASE WHEN i BETWEEN 60001 AND 60999 THEN ' boundary999' WHEN i BETWEEN 61001 AND 62000 THEN ' boundary1000'
 WHEN i BETWEEN 63001 AND 64001 THEN ' boundary1001' WHEN i>100000 THEN ' missingonlyneedle' ELSE '' END,
 CASE WHEN i<=50000 THEN CASE WHEN i%10=0 THEN 'Teresa Teng' ELSE '周杰倫' END ELSE 'Artist '||lpad((1+(i-1)%20000)::text,5,'0') END
 ||' / Artist '||lpad((1+(i+9999)%20000)::text,5,'0'),
 1,'10000000-0000-4000-8000-000000000001','track-'||i||'.flac',CASE WHEN i<=100000 THEN 'present' ELSE 'missing' END,
 NOW(),'flac_vorbis_comment','flac',180,16,44100,2 FROM generate_series(1,110000) i;
INSERT INTO track_credits(track_id,role,name,position)
SELECT t.id,r.role,CASE WHEN r.role='composer' AND t.position<=50000 THEN '久石譲'
 ELSE CASE WHEN n.i<=4000 THEN 'Alias ' ELSE 'Artist ' END||lpad(n.i::text,5,'0') END,r.position
FROM tracks t CROSS JOIN (VALUES('composer',1),('conductor',2),('performer',3),('producer',4)) r(role,position)
CROSS JOIN LATERAL (SELECT 1+(t.position-1+r.position*1000)%20000 AS i) n;
