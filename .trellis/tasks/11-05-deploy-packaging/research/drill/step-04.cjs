const {expect,http,save,launch,json,capture,privateWrite,safeFailure}=require('./browser-common.cjs');
(async()=>{
const {browser,context,page}=await launch('http-state.json');
try{
  console.log('步骤 4：通过页面添加容器目录 /music 并开始扫描。');
  await page.goto(http+'/library');
  await page.getByLabel('目录路径',{exact:true}).fill('/music');
  let pending=page.waitForResponse(r=>r.url().endsWith('/api/v1/library-roots')&&r.request().method()==='POST');
  await page.getByRole('button',{name:'添加目录',exact:true}).click();
  let response=await pending;expect(response.status()).toBe(201);
  console.log('POST /library-roots = 201。');
  pending=page.waitForResponse(r=>r.url().endsWith('/api/v1/scans')&&r.request().method()==='POST');
  await page.getByRole('button',{name:'开始扫描',exact:true}).click();
  response=await pending;expect(response.status()).toBe(202);
  const created=await response.json();
  let scan;const started=Date.now();
  do {
    scan=await json(await page.request.get(http+'/api/v1/scans/'+created.id),'扫描状态');
    if(scan.status!=='running')break;
    if(Date.now()-started>600000)throw new Error('扫描超过十分钟尚未结束');
    await page.waitForTimeout(1000);
  }while(true);
  console.log(JSON.stringify({scan_status:scan.status,elapsed_seconds:(Date.now()-started)/1000,diagnostics:scan.diagnostics,report_status:scan.report_status}));
  expect(scan.status).toBe('succeeded');
  const releases=await json(await page.request.get(http+'/api/v1/releases?page_size=1'),'发行总数');
  const groups=await json(await page.request.get(http+'/api/v1/release-groups?page_size=100'),'专辑组');
  console.log(JSON.stringify({releases:releases.pagination.total,album_groups:groups.pagination.total}));
  expect(groups.items.length).toBeGreaterThan(1);
  privateWrite('catalog-selection.json',{groupIds:groups.items.map(x=>x.id),representatives:groups.items.map(x=>({groupId:x.id,releaseId:x.representative.id})),scanId:created.id});
  await page.goto(http+'/library/scans/'+created.id);
  await expect(page.getByRole('heading',{name:'扫描报告',exact:true})).toBeVisible();
  await capture(page,'step-04-scan.png','scan');
  await save(context);
  console.log('结果：通过；扫描报告截图已遮挡真实专辑列表及诊断明细，选择标识仅存于 /tmp。');
}finally{await browser.close();}
})().catch(safeFailure);
