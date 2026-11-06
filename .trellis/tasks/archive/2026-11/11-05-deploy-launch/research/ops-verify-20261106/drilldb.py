from ops import *
T=pathlib.Path('/tmp/roomusic-drill')
def sql(q):
    return out(D+['exec','-T','postgres','psql','-X','-qAt','-U','roomusic','-d','roomusic','-v','ON_ERROR_STOP=1'],input='BEGIN TRANSACTION READ ONLY;\n'+q+'\nCOMMIT;\n')
def counts():
    tables=['schema_migrations','users','sessions','library_roots','releases','tracks','release_artworks','favorite_albums','personal_playlists','personal_playlist_items']
    q="SELECT 'schema_migrations.max|' || max(version) FROM schema_migrations;\n"+'\n'.join("SELECT '"+t+"|' || count(*) FROM "+t+';' for t in tables)
    return dict(x.split('|') for x in sql(q).splitlines())
def artwork_snapshot(name):
    q="""SELECT coalesce(json_agg(x ORDER BY release_id),'[]'::json) FROM (
    SELECT a.release_id,a.source_type,a.storage_key,a.content_hash,
      (SELECT count(*) FROM media m WHERE m.release_id=a.release_id) AS medium_count,
      EXISTS(SELECT 1 FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=a.release_id AND t.source_kind='cue_virtual') AS has_cue
    FROM release_artworks a) x;"""
    # 身份与存储键不打印；只保存演练私有目录，输出聚合。
    data=json.loads(sql(q));(T/name).write_text(json.dumps(data));os.chmod(T/name,0o600)
    categories={'embedded':lambda x:x['source_type']=='embedded','folder':lambda x:x['source_type']=='folder','cue':lambda x:x['has_cue'],'multi_disc':lambda x:x['medium_count']>1}
    return {'release_artworks':len(data),'distinct_resources':len({x['storage_key'] for x in data}),**{k:sum(f(x) for x in data) for k,f in categories.items()}}
