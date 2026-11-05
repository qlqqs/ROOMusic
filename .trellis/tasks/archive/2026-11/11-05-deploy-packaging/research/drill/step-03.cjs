const {expect,http,admin,save,launch,json,capture,safeFailure}=require('./browser-common.cjs');
(async()=>{
  const {browser,context,page}=await launch();
  try {
    console.log('步骤 3：打开 HTTP 演练入口，确认空库初始化页面。');
    const status=await json(await page.request.get(http+'/api/v1/setup/status'),'初始化状态');
    expect(status.setup_required).toBe(true);
    await page.goto(http);
    await expect(page.getByRole('heading',{name:'创建管理员',exact:true})).toBeVisible();
    await capture(page,'step-03-setup.png','auth');
    await page.getByLabel('用户名',{exact:true}).fill(admin.username);
    await page.getByLabel('密码',{exact:true}).fill(admin.password);
    await page.getByLabel('确认密码',{exact:true}).fill(admin.password);
    const pending=page.waitForResponse(r=>r.url().endsWith('/api/v1/setup/admin')&&r.request().method()==='POST');
    await page.getByRole('button',{name:'创建管理员并登录',exact:true}).click();
    const response=await pending;
    expect(response.status()).toBe(201);
    await expect.poll(async()=>(await page.request.get(http+'/api/v1/auth/me')).status()).toBe(200);
    await page.goto(http+'/library');
    await expect(page.getByLabel('目录路径',{exact:true})).toBeVisible();
    await save(context);
    await capture(page,'step-03-created.png','scan');
    console.log('POST /setup/admin = 201；GET /auth/me = 200；管理员创建并登录成功。');
    console.log('会话状态仅保存到 /tmp/roomusic-drill/http-state.json（600）；截图已遮罩。');
  } finally { await browser.close(); }
})().catch(safeFailure);
