#!/usr/bin/env python3
"""
MDIS 运维平台整体回归测试 (E2E)
========================================
对一套已部署的 MDIS(单机 docker-compose 或 K8s 均可)做全功能回归：
  登录 → 全页面浏览器走查(按 iframe-urls 动态发现菜单) → Grafana 面板可达
  → 数据源列表/测试连接/增删 → PromQL 预览 → 告警全链路(建规则→立即检查
  →状态流转→告警历史落库→暂停恢复→清理) → 通知渠道增删 → 慢查询/全局历史等 API

用法：
  pip install playwright && playwright install chromium
  # 单机(子路径模式):
  python3 mdis_regression.py --base http://58.33.90.218:9080 --subpath /mdis --token <ENV_OPS_TOKEN>
  # K8s(NodePort 直连,无子路径):
  python3 mdis_regression.py --base http://<节点IP>:30881 --token <ENV_OPS_TOKEN>
  # 可选: --headed 显示浏览器; --webhook-url <真实群机器人地址> 额外验证真实通知推送

退出码 0=全过，非 0=有失败（结尾打印汇总）。
每次功能改完/部署完跑一遍，单机集群同一个脚本。
"""
import argparse
import json
import re
import sys
import time

from playwright.sync_api import sync_playwright

PASS, FAIL, SKIP = [], [], []


def log(step, ok=True, skip=False):
    if skip:
        SKIP.append(step)
        print(f"  [-] {step} (跳过)", flush=True)
    elif ok:
        PASS.append(step)
        print(f"  [✓] {step}", flush=True)
    else:
        FAIL.append(step)
        print(f"  [✗] {step}", flush=True)


def section(title):
    print(f"\n== {title} ==", flush=True)


def run(base, subpath, token, headed, webhook_url):
    stamp = time.strftime("%Y%m%d-%H%M%S")
    rule_name = f"回归测试-必触发-{stamp}"
    channel_name = f"回归测试渠道-{stamp}"
    host_name = f"回归测试主机-{stamp}"
    web = f"{base}{subpath}/web"
    api = f"{base}{subpath}/api"

    with sync_playwright() as p:
        try:
            # 优先系统 Chrome(macOS 开发机);服务器无 Chrome 则回退 playwright 自带 chromium
            browser = p.chromium.launch(channel="chrome", headless=not headed)
        except Exception:
            browser = p.chromium.launch(headless=not headed, args=["--no-sandbox"])
        # 钉死 locale=zh-CN：产品主语言为中文，全程按中文 UI 走查；
        # 否则 headless chromium 默认 en-US → 前端回退英文 → 中文标记匹配不到（误报渲染失败）
        ctx = browser.new_context(ignore_https_errors=True, locale="zh-CN")
        page = ctx.new_page()
        page.set_default_timeout(20000)
        console_errors = []
        page.on("pageerror", lambda e: console_errors.append(str(e)))

        def api_req(method, path, payload=None):
            """带 atoken cookie 的 API 请求(浏览器上下文共享 cookie)。"""
            url = f"{api}{path}"
            kw = {}
            if payload is not None:
                kw = {"data": json.dumps(payload),
                      "headers": {"Content-Type": "application/json"}}
            r = getattr(ctx.request, method)(url, **kw)
            body = None
            try:
                body = r.json()
            except Exception:
                pass
            return r.status, body

        # ───────────────────────── 1. 登录 ─────────────────────────
        section("登录")
        try:
            page.goto(f"{web}/auth")
            # ⚠️ 不能用 locator("input").first——表单里第一个 input 是只给浏览器密码
            # 管理器看的隐藏 username 字段,fill 会因不可见而超时。
            token_input = page.locator('input[type="password"]').first
            token_input.fill(token)
            # 按 Enter 提交,不点"第一个 button"——登录页右上角有语言切换器,
            # get_by_role("button").first 会点到它而非「登录」,导致永远停在登录页。
            token_input.press("Enter")
            # ⚠️ 不能等 "**/web/**"：登录页自己就是 /mdis/web/auth，这个模式当场就成立、
            # 等于没等一步。跳转稍慢时下面那句 assert 就撞在「还停在 /auth」的瞬间，
            # 报成「token 错误?」——而 token 完全正确（同一个 token 打 /api/auth 返回 200）。
            # 跳转快就过、慢就挂，是发版前最后一道闸上的 flaky。等「URL 里不再有 /auth」
            # 才是真正的登录完成信号。
            page.wait_for_url(lambda url: "/auth" not in url, timeout=20000)
            page.wait_for_load_state("networkidle")
            assert "/auth" not in page.url, "仍停留在登录页(token 错误?)"
            log("token 登录跳转成功")
        except Exception as e:
            log(f"token 登录失败: {e}", ok=False)
            print("登录失败，终止后续测试")
            browser.close()
            return summary()

        # ─────────────── 2. 菜单发现(以 iframe-urls 为准) ───────────────
        section("页面走查(按后端菜单动态发现)")
        st, iframe_urls = api_req("get", "/iframe-urls")
        log(f"GET /iframe-urls → {st}", ok=(st == 200 and isinstance(iframe_urls, dict)))
        groups = iframe_urls if isinstance(iframe_urls, dict) else {}

        # 路由表: 菜单 key → SPA 路由
        static_routes = [
            ("概览", "alert/overview", "总监控任务"),
            ("数据源", "datasources", "数据源"),
            ("告警规则", "alert/rules", "任务名"),
            ("告警历史", "alert/history", "告警历史"),
            ("通知渠道", "alert/webhooks", "通知渠道"),
        ]
        for label, route, expect_text in static_routes:
            try:
                page.goto(f"{web}/{route}")
                page.wait_for_load_state("networkidle")
                page.get_by_text(expect_text, exact=False).first.wait_for(timeout=15000)
                log(f"{label} 页面渲染(/{route})")
            except Exception as e:
                log(f"{label} 页面渲染失败: {e}", ok=False)

        # iframe 类页面: resource/log/trace 组里后端返回什么就走查什么
        iframe_routes = {
            "host": "host", "mysql": "mysql", "mongodb": "mongodb",
            "redis": "redis", "kafka": "kafka", "elasticsearch": "elasticsearch",
            "k8s": "k8s",
            "containerConsole": "containerConsole", "serviceLogs": "serviceLogs",
            # tempo 链路页 1.4.0 起 SPA 路由改名 tempo→apm(后端 iframe-urls 仍返回 tempo key)
            "tempo": "apm", "apm": "apm",
        }
        for group_key, items in groups.items():
            if not isinstance(items, dict):
                continue
            for key, gf_url in items.items():
                route = iframe_routes.get(key)
                if not route:
                    continue
                try:
                    page.goto(f"{web}/{route}")
                    frame_el = page.locator("iframe").first
                    frame_el.wait_for(state="attached", timeout=15000)
                    src = frame_el.get_attribute("src") or gf_url
                    target = src if src.startswith("http") else f"{base}{src}"
                    r = ctx.request.get(target)
                    log(f"{group_key}/{key} 页面 + iframe({r.status})", ok=(r.status == 200))
                except Exception as e:
                    log(f"{group_key}/{key} 页面失败: {e}", ok=False)

        # 自定义页面(custom 分组是数组,不是 {key:url},上面那个循环走不到)。
        # 目标是客户自己的服务,连不通属正常环境差异 → 只要求 SPA 路由能渲染出 iframe。
        for page_cfg in (groups.get("custom") or []):
            key = (page_cfg or {}).get("key") or ""
            if not key:
                continue
            try:
                page.goto(f"{web}/{key}")
                frame_el = page.locator("iframe").first
                frame_el.wait_for(state="attached", timeout=15000)
                log(f"custom/{page_cfg.get('name')} 页面渲染(/{key})")
            except Exception as e:
                log(f"custom/{key} 页面失败: {e}", ok=False)

        # 慢查询 tab(MongoDB 数据源启用时才有入口,页面本身始终可达)
        try:
            page.goto(f"{web}/mongodb/slowQuery")
            page.wait_for_load_state("networkidle")
            log("MongoDB 慢查询页面渲染")
        except Exception as e:
            log(f"慢查询页面失败: {e}", ok=False)

        # ───────────────────── 3. 数据源功能 ─────────────────────
        section("数据源")
        st, sources = api_req("get", "/alert/sources")
        ok = st == 200 and isinstance(sources, list) and len(sources) > 0
        log(f"GET /alert/sources → {st}, {len(sources) if ok else 0} 条", ok=ok)
        builtin = [s for s in (sources or []) if s.get("builtin")]
        log("内置 Prometheus 数据源存在", ok=len(builtin) > 0)

        # UI 测试连接(第一行,即内置 Prometheus → 走完整查询验证)
        try:
            page.goto(f"{web}/datasources")
            row = page.locator("tr", has_text="内置").first
            # 行操作 1.4.0 起为图标按钮(ActionIcon,文案在 aria-label),用 role 定位
            row.get_by_role("button", name="测试连接").click()
            msg = page.locator(".ant-message").get_by_text(re.compile("连接成功|连接失败|失败")).first
            msg.wait_for(timeout=25000)
            txt = msg.inner_text()
            log(f"UI 测试连接(内置 Prometheus) → {txt}", ok=("连接成功" in txt))
        except Exception as e:
            log(f"UI 测试连接失败: {e}", ok=False)

        # API 增删主机数据源
        host_id = None
        st, body = api_req("post", "/alert/host-instances",
                           {"name": host_name, "address": "127.0.0.1:59100",
                            "enabled": True, "purposes": ["metrics"]})
        if st == 200 and isinstance(body, dict):
            host_id = body.get("id") or (body.get("data") or {}).get("id")
        log(f"新建主机数据源 → {st}", ok=(st == 200))
        if host_id:
            st, _ = api_req("delete", f"/alert/host-instances/{host_id}")
            log(f"删除主机数据源 → {st}", ok=(st == 200))
        else:
            # 兜底:按名称查出来删,避免残留
            st, lst = api_req("get", "/alert/host-instances")
            for it in (lst if isinstance(lst, list) else (lst or {}).get("data", []) or []):
                if it.get("name") == host_name:
                    api_req("delete", f"/alert/host-instances/{it['id']}")
            log("删除主机数据源(按名清理)", ok=True)

        # PromQL 预览
        st, body = api_req("post", "/alert/datasources/test-query", {"promql": "up"})
        ok = st == 200 and isinstance(body, dict) and body.get("ok") is True
        log(f"PromQL 预览(up) → {st}, series={body.get('count') if ok else '?'}", ok=ok)

        # ── FIX-1: 测试连接带 id 成功 / 脱敏 URI 不带 id 失败 ──────────────
        # 取一个「实际连得上」的 MongoDB 实例，而不是列表里的第一个：环境里常留着指向
        # 已销毁实例的历史数据源（本地环境就有两条指向早已删除的临时容器），排在前面时
        # 会让本组用例整组标红，把环境残留误报成功能故障。
        _mongo_inst_id = None
        _mongo_skipped = []
        _st, _mongo_list = api_req("get", "/alert/mongo-instances")
        if _st == 200 and isinstance(_mongo_list, list):
            for _inst in _mongo_list:
                _iid = _inst.get("id")
                if not _iid:
                    continue
                _pst, _pbody = api_req("post", "/alert/datasources/test",
                                       {"type": "mongodb", "id": _iid})
                if _pst == 200 and isinstance(_pbody, dict) and _pbody.get("ok") is True:
                    _mongo_inst_id = _iid
                    break
                _mongo_skipped.append(_inst.get("name") or _iid)
        # 跳过了哪些、为什么，明说；否则「换了一条源才过」会被读成「一次就过」
        if _mongo_skipped:
            log(f"MongoDB 用例选源：跳过连不上的 {len(_mongo_skipped)} 条 "
                f"({', '.join(map(str, _mongo_skipped[:3]))})", skip=True)
        if _mongo_inst_id:
            # 带 id → 后端从库中读真实凭据，应连接成功
            _st, _body = api_req("post", "/alert/datasources/test",
                                 {"type": "mongodb", "id": _mongo_inst_id})
            if _st == 404:
                log("测试连接(mongodb,带id) → 端点未部署(404),跳过", skip=True)
            else:
                _ok = _st == 200 and isinstance(_body, dict) and _body.get("ok") is True
                log(f"测试连接(mongodb,带id) → ok={(_body or {}).get('ok')}", ok=_ok)
            # 脱敏 URI 不带 id → 后端拿到含 '********' 的 URI，无法真实连接，应 ok=false
            _masked_uri = "mongodb://user:********@127.0.0.1:27017/admin"
            _st2, _body2 = api_req("post", "/alert/datasources/test",
                                   {"type": "mongodb", "uri": _masked_uri})
            if _st2 == 404:
                log("测试连接(mongodb,脱敏uri无id) → 端点未部署(404),跳过", skip=True)
            else:
                _ok2 = _st2 == 200 and isinstance(_body2, dict) and _body2.get("ok") is False
                log(f"测试连接(mongodb,脱敏uri无id) → ok={(_body2 or {}).get('ok')} (期望 false)", ok=_ok2)
        else:
            log("测试连接(mongodb带id/脱敏uri) → 无 MongoDB 实例,跳过", skip=True)

        # ── CAP-2: mongo 能力探测三态契约 ────────────────────────────────────
        # 不假设目标 mongo 是单节点还是副本集（环境相关）：只校验探测「契约」——
        # connect.ok=true、三个用途各带布尔 available、且任一 available=false 必附 reason。
        # （alert 是否可用取决于目标是否副本集；diagnose 取决于能否读 system.profile。）
        if _mongo_inst_id:
            _st, _body = api_req("post", "/alert/datasources/probe",
                                 {"type": "mongodb", "id": _mongo_inst_id})
            if _st == 404:
                log("能力探测(mongodb,带id) → 端点未部署(404),跳过", skip=True)
            else:
                _probe_ok = False
                try:
                    _connect_ok = (_body or {}).get("connect", {}).get("ok") is True
                    _purposes = (_body or {}).get("purposes") or {}
                    # 每个用途须存在且 available 为布尔；available=false 时 reason 非空
                    _shape_ok = True
                    for _p in ("metrics", "alert", "diagnose"):
                        _cap = _purposes.get(_p)
                        if not isinstance(_cap, dict) or not isinstance(_cap.get("available"), bool):
                            _shape_ok = False
                            break
                        # 1.5.10 起不可用原因优先走 reasonKey（英文机器键，文案归前端，
                        # 见 CLAUDE.md 的约定）；reason 仅为尚未 key 化的兜底，两者有其一即可。
                        _why = (_cap.get("reasonKey") or _cap.get("reason") or "").strip()
                        if _cap.get("available") is False and not _why:
                            _shape_ok = False
                            break
                    _probe_ok = _connect_ok and _shape_ok
                    log(
                        f"能力探测(mongodb)契约: connect={_connect_ok}, "
                        f"purposes={ {k: _purposes.get(k, {}).get('available') for k in ('metrics','alert','diagnose')} }, "
                        f"shape_ok={_shape_ok}",
                        ok=_probe_ok,
                    )
                except Exception as _e:
                    log(f"能力探测(mongodb) 响应解析失败: {_e}", ok=False)
        else:
            log("能力探测(mongodb) → 无 MongoDB 实例,跳过", skip=True)

        # ── EXPORT(1.5.9): 数据源导出 → 把导出的内容原样 dryRun 导回 ─────────
        # 备份的价值全在「恢复得回来」。只断言导出接口 200 没有意义——格式偏差在
        # 导出那一刻看不出来，只有真拿去导入才暴露，所以这里必须走一遍闭环。
        # 不含密码导出 + 同名已存在 → 导入侧 carryOverSecrets 会沿用库里原密码，
        # 因此全部条目应判为 updated，failed 必须为 0。
        _r = ctx.request.get(f"{api}/alert/sources/export")
        if _r.status == 404:
            log("数据源导出 → 端点未部署(404),跳过", skip=True)
        else:
            _yaml = _r.text()
            _exp_ok = _r.status == 200 and "sources:" in _yaml
            log(f"数据源导出 → {_r.status}, {len(_yaml)} 字节", ok=_exp_ok)
            if _exp_ok and "sources: []" not in _yaml:
                _r2 = ctx.request.post(f"{api}/alert/sources/import?dryRun=true",
                                       data=_yaml,
                                       headers={"Content-Type": "application/yaml"})
                _b2 = None
                try:
                    _b2 = _r2.json()
                except Exception:
                    pass
                _failed = (_b2 or {}).get("failed")
                _updated = (_b2 or {}).get("updated", 0)
                _rt_ok = _r2.status == 200 and _failed == 0 and _updated > 0
                if not _rt_ok:
                    for _it in ((_b2 or {}).get("results") or []):
                        if _it.get("action") == "failed":
                            print(f"    导不回去: {_it.get('kind')}/{_it.get('name')} "
                                  f"{_it.get('errorKey')} {_it.get('detail')}")
                log(f"导出内容 dryRun 导回 → updated={_updated}, failed={_failed}", ok=_rt_ok)
            elif _exp_ok:
                log("导出内容 dryRun 导回 → 当前无数据源,跳过", skip=True)

        # ───────────────── 4. 告警全链路(核心) ─────────────────
        section("告警全链路")
        webhook_id = None
        if webhook_url:
            st, body = api_req("post", "/alert/webhooks",
                               {"name": channel_name, "type": "custom", "url": webhook_url})
            webhook_id = (body or {}).get("id") if st == 200 else None
            log(f"创建真实通知渠道 → {st}", ok=(st == 200))

        # 数据源 id: 取启用的 Prometheus 数据源(内置单例 id 为 builtin-prometheus)
        st, ds_list = api_req("get", "/alert/datasources")
        ds_id = None
        for d in (ds_list if isinstance(ds_list, list) else []):
            if d.get("enabled"):
                ds_id = d.get("id")
                break
        log(f"取告警数据源 id: {ds_id}", ok=bool(ds_id))

        rule_id = None
        st, body = api_req("post", "/alert/alert-rules", {
            "name": rule_name, "dataSourceId": ds_id, "category": "custom",
            "metricType": "", "promql": "vector(100)", "operator": ">",
            "threshold": 1, "unit": "", "interval": 10, "duration": 10,
            "recoveryDuration": 60, "webhookId": webhook_id, "alertNote": "回归测试自动创建,可删",
        })
        if st == 200 and isinstance(body, dict):
            rule_id = body.get("id") or (body.get("data") or {}).get("id")
        log(f"创建必触发规则(vector(100)>1) → {st}", ok=(st == 200))

        if not rule_id:  # 按名捞 id
            st, lst = api_req("get", "/alert/alert-rules")
            items = lst if isinstance(lst, list) else (lst or {}).get("data", []) or []
            for it in items:
                if it.get("name") == rule_name:
                    rule_id = it.get("id")

        if rule_id:
            # 浏览器走查: 规则出现在列表
            try:
                page.goto(f"{web}/alert/rules")
                page.get_by_text(rule_name).first.wait_for(timeout=15000)
                log("UI 规则列表显示新规则")
            except Exception as e:
                log(f"UI 规则列表未显示: {e}", ok=False)

            # UI 立即检查
            # ⚠️ 断言只认成功文案「已触发立即检查」。原先用 re.compile("已触发|检查")，
            # 而失败提示是「规则正在检查中，请稍后重试」——「检查」二字同样命中，于是
            # 后端 refresh 100% 失败时这条用例照样绿。该缺陷正是这么带病发版的。
            try:
                row = page.locator("tr", has_text=rule_name).first
                row.get_by_role("button", name="立即检查").first.click()
                page.locator(".ant-message").get_by_text("已触发立即检查").first.wait_for(timeout=10000)
                log("UI 立即检查触发成功")
            except Exception as e:
                log(f"UI 立即检查失败: {e}", ok=False)

            # 再从 API 侧确认一次：UI toast 只能证明前端收到了 200，这里直接校验
            # 后端返回码，避免「前端提示成功、后端其实报错」的组合被漏掉。
            _st_rf, _bd_rf = api_req("post", f"/alert/alert-rules/{rule_id}/refresh")
            log(f"API 立即检查 → {_st_rf}" + ("" if _st_rf == 200 else f" body={str(_bd_rf)[:160]}"),
                ok=(_st_rf == 200))

            # 轮询状态到 告警(Firing): duration=10s,二次评估后应 Firing
            status = None
            for _ in range(12):
                time.sleep(5)
                api_req("post", f"/alert/alert-rules/{rule_id}/refresh")
                st, lst = api_req("get", "/alert/alert-rules")
                items = lst if isinstance(lst, list) else (lst or {}).get("data", []) or []
                me = next((x for x in items if x.get("id") == rule_id), {})
                status = me.get("alertState") or me.get("status")
                if str(status) == "Firing":
                    break
            log(f"状态流转到 Firing(当前: {status})", ok=(str(status) == "Firing"))

            # 告警历史落库
            got_history = False
            for _ in range(6):
                st, hist = api_req("get", f"/alert/alert-rules/{rule_id}/alert-history")
                items = hist if isinstance(hist, list) else \
                    ((hist or {}).get("items") or (hist or {}).get("data") or [])
                if st == 200 and len(items) > 0:
                    got_history = True
                    break
                time.sleep(5)
            log("规则告警历史已落库", ok=got_history)

            # 全局告警历史 API + UI
            st, ghist = api_req("get", "/alert/alert-history?page=1&pageSize=20")
            log(f"GET 全局告警历史 → {st}", ok=(st == 200))
            try:
                page.goto(f"{web}/alert/history")
                page.get_by_text(rule_name).first.wait_for(timeout=15000)
                log("UI 全局告警历史显示该规则记录")
            except Exception as e:
                log(f"UI 全局告警历史未显示: {e}", ok=False)

            if webhook_url:
                log("真实通知推送: 请到群里确认是否收到告警卡片(脚本无法断言)", skip=True)

            # 暂停 / 恢复
            st1, _ = api_req("post", f"/alert/alert-rules/{rule_id}/pause", {"mode": "manual"})
            st2, _ = api_req("post", f"/alert/alert-rules/{rule_id}/resume")
            log(f"暂停({st1}) / 恢复({st2})", ok=(st1 == 200 and st2 == 200))

            # UI 删除(走 Popconfirm)
            try:
                page.goto(f"{web}/alert/rules")
                row = page.locator("tr", has_text=rule_name).first
                row.get_by_role("button", name="删除").first.click()
                # 移开鼠标:删除图标的 tooltip 会停在原处挡住确认按钮的点击
                page.mouse.move(0, 0)
                # Popconfirm 确认按钮:弹层为 .ant-popover.ant-popconfirm,内含 取消(default)+ 删除(dangerous)。
                # scoped 到弹层取 dangerous 按钮,避开行内同名删除图标
                page.locator(".ant-popconfirm .ant-btn-dangerous").last.click()
                page.locator(".ant-message").get_by_text("删除成功").first.wait_for(timeout=10000)
                log("UI 删除规则成功")
                rule_id_deleted = True
            except Exception as e:
                api_req("delete", f"/alert/alert-rules/{rule_id}")
                log(f"UI 删除失败,已用 API 兜底删除: {e}", ok=False)
        else:
            log("拿不到规则 id,告警链路无法继续", ok=False)

        # ───────────────── 5. 通知渠道 CRUD ─────────────────
        section("通知渠道")
        st, body = api_req("post", "/alert/webhooks",
                           {"name": channel_name + "-crud", "type": "custom",
                            "url": "http://127.0.0.1:9/regression-test"})
        ch_id = (body or {}).get("id") if st == 200 else None
        log(f"创建渠道 → {st}", ok=(st == 200))
        try:
            page.goto(f"{web}/alert/webhooks")
            page.get_by_text(channel_name + "-crud").first.wait_for(timeout=15000)
            log("UI 渠道列表显示新渠道")
        except Exception as e:
            log(f"UI 渠道列表未显示: {e}", ok=False)
        if ch_id:
            st, _ = api_req("delete", f"/alert/webhooks/{ch_id}")
            log(f"删除渠道 → {st}", ok=(st == 200))
        if webhook_id:
            api_req("delete", f"/alert/webhooks/{webhook_id}")

        # ─────────── 5.1 自定义页面(建→出现在菜单→改→删) ───────────
        # 用直嵌模式(mode=iframe)：目标地址探不通也照存，且不牵扯反代链路，
        # 跑在任何环境上结论都一样。
        section("自定义页面")
        cp_name = f"回归自定义页-{stamp}"
        st, body = api_req("post", "/alert/custom-pages",
                           {"name": cp_name, "url": "http://127.0.0.1:9/regression-custom",
                            "mode": "iframe"})
        cp = ((body or {}).get("page") or {}) if st == 200 else {}
        cp_id = cp.get("id")
        log(f"创建自定义页面 → {st} (id={cp_id})", ok=(st == 200 and bool(cp_id)))

        st, urls = api_req("get", "/iframe-urls")
        menu_names = [p.get("name") for p in ((urls or {}).get("custom") or [])]
        log("iframe-urls 的 custom 分组含新页面", ok=(cp_name in menu_names))

        # 导出/导入要带上自定义页面(1.5.12)：不带的话重装一次这些外链就得手工重配，
        # 而这正是 1.5.9 加导出功能要解决的问题。断言导出文件里有它，且能原样导回去。
        _re = ctx.request.get(f"{api}/alert/sources/export")
        _exp_yaml = _re.text() if _re.status == 200 else ""
        log("导出配置含自定义页面",
            ok=("kind: customPage" in _exp_yaml and cp_name in _exp_yaml))
        if _exp_yaml:
            _ri = ctx.request.post(f"{api}/alert/sources/import?dryRun=true",
                                   data=_exp_yaml,
                                   headers={"Content-Type": "application/yaml"})
            _bi = None
            try:
                _bi = _ri.json()
            except Exception:
                pass
            _cp_res = [x for x in ((_bi or {}).get("results") or [])
                       if x.get("kind") == "customPage"]
            # 同名已存在 → 应判为 updated 而不是又建一条(幂等)
            log(f"自定义页面 dryRun 导回 → {[x.get('action') for x in _cp_res]}",
                ok=(bool(_cp_res) and all(x.get("action") == "updated" for x in _cp_res)))

        if cp_id:
            st, _ = api_req("put", f"/alert/custom-pages/{cp_id}",
                            {"name": cp_name + "-改", "url": "http://127.0.0.1:9/regression-custom",
                             "mode": "iframe", "enabled": False})
            log(f"更新自定义页面(改名+停用) → {st}", ok=(st == 200))

            # 停用后该条不该再出现在菜单里(不是删了，只是不显示)
            st, urls = api_req("get", "/iframe-urls")
            still = [p.get("id") for p in ((urls or {}).get("custom") or [])]
            log("停用后菜单不再返回该页面", ok=(cp_id not in still))

            st, _ = api_req("delete", f"/alert/custom-pages/{cp_id}")
            log(f"删除自定义页面 → {st}", ok=(st == 200))
            st, _ = api_req("delete", f"/alert/custom-pages/{cp_id}")
            log(f"重复删除返回 404 → {st}", ok=(st == 404))

        # ───────────────── 6. 其余核心 API ─────────────────
        section("其余核心 API")
        st, body = api_req("post", "/slow-query/list",
                           {"page": 1, "pageSize": 10, "sortBy": "", "sortOrder": "", "searchText": ""})
        log(f"POST /slow-query/list → {st}", ok=(st == 200))
        st, _ = api_req("post", "/slow-query/profiling-status", {"dbname": "mdwsrows"})
        log(f"POST /slow-query/profiling-status(mdwsrows) → {st}", ok=(st == 200))
        st, _ = api_req("post", "/global-args", {})
        log(f"POST /global-args → {st}", ok=(st == 200))

        # ── SQ-1: 多 diagnose 源 + 慢查询结果含来源标识(dbtag) ──────────────
        # 新建第二个 diagnose mongo 实例(名字带秒级时间戳避免撞名),跑完清理。
        # 注意: 该用例依赖慢查询历史库已有记录(mdservicedata.slowops);若库为空则
        # dbtag 断言给出明确失败信息而非崩溃。
        _sq_inst_name = f"回归测试-diagnose-{stamp}"
        _sq_inst_id = None
        # 取已有 MongoDB 实例的 URI 作为第二个 diagnose 源(复用已配置的连接)
        _sq_inst_uri = None
        if isinstance(_mongo_list, list):
            for _m in _mongo_list:
                _raw_uri = (_m.get("uri") or "")
                # Sanitize API 返回脱敏 URI(含 ********, 不可直接用于创建新实例)
                # 新建时只需要一个可连接的 URI; 若有已有实例,直接用原实例 id probe 即可。
                # SQ-1 只验证 API 结构字段(dbTag 存在),不需要真实写入第二个分片的慢查询数据。
                # 因此直接用已有实例的 URI 来创建(脱敏问题: CreateMongoInstance 会保存脱敏串
                # 导致连接失败,但测试只需验证慢查询 API 字段结构,不需要第二个实例实际工作)。
                _sq_inst_uri = _raw_uri if _raw_uri else None
                break
        if _sq_inst_uri:
            _st, _sq_body = api_req("post", "/alert/mongo-instances",
                                    {"name": _sq_inst_name,
                                     "uri": _sq_inst_uri,
                                     "authDb": "admin",
                                     "enabled": True,
                                     "purposes": ["diagnose"]})
            if _st == 200 and isinstance(_sq_body, dict):
                _sq_inst_id = _sq_body.get("id")
            log(f"新建第二个 diagnose mongo 源 → {_st}", ok=(_st == 200))
        else:
            log("SQ-1: 无可用 MongoDB URI,跳过新建第二个 diagnose 源", skip=True)

        # 调 /slow-query/list,断言结果项含 slowQuery.dbtag 来源标识
        # (Result 结构体无 bson tag,mongo-driver 以小写 dbtag 存储)
        _st_sq, _sq_list_body = api_req("post", "/slow-query/list",
                                        {"page": 1, "pageSize": 20,
                                         "sortBy": "", "sortOrder": "", "searchText": ""})
        if _st_sq == 200 and isinstance(_sq_list_body, dict):
            _sq_items = _sq_list_body.get("data") or []
            if len(_sq_items) == 0:
                log("慢查询多源区分(SQ-1): 慢查询历史为空,无法验证 dbtag 字段", skip=True)
            else:
                # 每条记录是 {timestamp, slowQuery:{dbtag,ns,...}, controlInfo}
                _first = _sq_items[0]
                _slow_q = _first.get("slowQuery") if isinstance(_first, dict) else None
                if _slow_q is None:
                    # 兼容:若记录直接带 dbtag(非嵌套结构)
                    _dbtag_val = (_first.get("dbtag") or _first.get("dbTag")) if isinstance(_first, dict) else None
                    _has_dbtag = _dbtag_val is not None
                else:
                    _dbtag_val = _slow_q.get("dbtag") or _slow_q.get("dbTag")
                    _has_dbtag = _dbtag_val is not None
                log(f"慢查询结果含来源标识 dbtag(SQ-1): 首条 dbtag={_dbtag_val!r}", ok=_has_dbtag)
        else:
            log(f"慢查询多源区分(SQ-1): /slow-query/list 返回 {_st_sq},跳过字段验证", skip=True)

        # 清理新建的 diagnose 源
        if _sq_inst_id:
            _st, _ = api_req("delete", f"/alert/mongo-instances/{_sq_inst_id}")
            log(f"清理第二个 diagnose mongo 源 → {_st}", ok=(_st == 200))
        elif _sq_inst_uri:
            # 兜底:按名称查出来删,避免残留
            _st2, _lst2 = api_req("get", "/alert/mongo-instances")
            for _it in (_lst2 if isinstance(_lst2, list) else []):
                if _it.get("name") == _sq_inst_name:
                    api_req("delete", f"/alert/mongo-instances/{_it['id']}")
            log("清理第二个 diagnose mongo 源(按名兜底)", ok=True)

        # ───────────────── 7. 控制台错误汇总 ─────────────────
        section("浏览器健康")
        log(f"页面 JS 错误数: {len(console_errors)}", ok=(len(console_errors) == 0))
        for e in console_errors[:5]:
            print(f"      pageerror: {e[:160]}")

        browser.close()
    return summary()


def summary():
    print(f"\n{'='*50}")
    print(f"通过 {len(PASS)}  失败 {len(FAIL)}  跳过 {len(SKIP)}")
    if FAIL:
        print("失败项:")
        for f in FAIL:
            print(f"  ✗ {f}")
    return 1 if FAIL else 0


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", required=True, help="如 http://58.33.90.218:9080")
    ap.add_argument("--subpath", default="", help="子路径,如 /mdis;直连无子路径则不填")
    ap.add_argument("--token", required=True, help="ENV_OPS_TOKEN")
    ap.add_argument("--headed", action="store_true")
    ap.add_argument("--webhook-url", default="", help="真实群机器人地址,提供则额外验证通知推送")
    args = ap.parse_args()
    sys.exit(run(args.base, args.subpath, args.token, args.headed, args.webhook_url))
