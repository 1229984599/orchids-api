import os
import time
import random
import string
import re
import requests
import html
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry

import undetected_chromedriver as uc
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC
from selenium.common.exceptions import TimeoutException, NoSuchElementException

# ================= 配置区 =================
MAIL_API = "https://mail.chatgpt.org.uk"
MAIL_KEY = "gpt-test"  
OUTPUT_FILE = os.path.abspath("orchids_accounts.txt")
TARGET_URL = "https://www.orchids.app/"

# ================= 工具函数 =================

def log(msg, level="INFO"):
    print(f"[{time.strftime('%H:%M:%S')}] [{level}] {msg}")

def create_http_session():
    s = requests.Session()
    s.mount("https://", HTTPAdapter(max_retries=Retry(total=3, backoff_factor=1)))
    return s

http = create_http_session()

def generate_password():
    chars = string.ascii_letters + string.digits + "!@#$%"
    return ''.join(random.choice(chars) for _ in range(14))

def upload_to_server(client_key):
    """将获取到的 cookie 上传到服务器. 这里是当前服务的地址"""
    url = "http://127.0.0.1:3002/api/accounts"
    payload = {
        "name": "我的账号",
        "client_cookie": client_key,
        "weight": 1,
        "enabled": True
    }
    try:
        log(f"🚀 正在上传 Key 到服务器...")
        r = requests.post(url, json=payload, auth=("admin", "admin123"), timeout=20)
        # 兼容 200 和 201 状态码
        if r.status_code in [200, 201]:
            log("✅ 服务器保存成功")
        else:
            log(f"❌ 服务器保存失败: {r.status_code} - {r.text}", "ERR")
    except Exception as e:
        log(f"❌ 上传过程发生异常: {e}", "ERR")

def create_temp_email():
    try:
        log("正在申请临时邮箱...")
        r = http.get(f"{MAIL_API}/api/generate-email", headers={"X-API-Key": MAIL_KEY}, timeout=20)
        if r.json().get('success'): 
            email = r.json()['data']['email']
            log(f"获取邮箱: {email}")
            return email
    except Exception as e:
        log(f"邮箱申请失败: {e}")
    return None

def wait_for_code(email):
    log(f"📩 正在监听 {email} 的收件箱 (120s)...")
    start = time.time()
    processed_ids = set()
    regex_strict = r'(?<!\d)(\d{6})(?!\d)' # 严格匹配独立的6位数字
    
    while time.time() - start < 120:
        try:
            r = http.get(f"{MAIL_API}/api/emails", params={"email": email}, headers={"X-API-Key": MAIL_KEY}, timeout=10)
            data = r.json().get('data', {}).get('emails', [])
            
            if data:
                latest_email = data[0]
                email_id = latest_email.get('id')
                
                if email_id not in processed_ids:
                    processed_ids.add(email_id)
                    subject = latest_email.get('subject', '')
                    raw_content = latest_email.get('content') or latest_email.get('html_content') or ''
                    
                    # HTML 清洗
                    text_content = html.unescape(raw_content)
                    text_content = re.sub(r'<[^>]+>', ' ', text_content)
                    text_content = re.sub(r'\s+', ' ', text_content).strip()
                    
                    log(f"📨 收到新邮件: {subject}")
                    
                    # 匹配验证码
                    for source in [subject, text_content]:
                        match = re.search(regex_strict, source)
                        if match:
                            code = match.group(1)
                            log(f"✅ 提取到验证码: {code}")
                            return code
            
            time.sleep(3)
        except: pass
            
    return None

def force_inject_turnstile(driver):
    """暴力且持续地尝试点击 Cloudflare 验证框"""
    try:
        # 先尝试在主文档中寻找可能外露的验证元素
        js_find_and_click = """
        function findAndClick() {
            let clicked = false;
            // 1. 深度搜索 shadowRoot
            function searchShadow(root) {
                if (clicked) return;
                let cb = root.querySelector('input[type="checkbox"], #turnstile-indicator, .ctp-checksum-container');
                if (cb) { cb.click(); clicked = true; return; }
                let all = root.querySelectorAll('*');
                for (let el of all) {
                    if (el.shadowRoot) searchShadow(el.shadowRoot);
                }
            }
            searchShadow(document);
            if (clicked) return true;

            // 2. 查找特定的 Turnstile 容器
            let selectors = ['#cf-turnstile', '#turnstile-wrapper', '.cf-turnstile'];
            for (let s of selectors) {
                let el = document.querySelector(s);
                if (el && el.shadowRoot) {
                    let cb = el.shadowRoot.querySelector('input[type="checkbox"]');
                    if (cb) { cb.click(); return true; }
                }
            }
            return false;
        }
        return findAndClick();
        """
        if driver.execute_script(js_find_and_click):
            log("🎯 主页面命中验证码点击")
            return True

        # 遍历所有 iframe 尝试点击
        iframes = driver.find_elements(By.TAG_NAME, "iframe")
        for index, frame in enumerate(iframes):
            try:
                driver.switch_to.frame(frame)
                if driver.execute_script(js_find_and_click):
                    log(f"🎯 Frame {index} 命中验证码点击")
                    driver.switch_to.default_content()
                    return True
                driver.switch_to.default_content()
            except:
                driver.switch_to.default_content()
    except Exception as e:
        log(f"暴力点击执行异常: {e}", "WARN")
    return False

# ================= 单次注册任务逻辑 =================

def register_one_account(current_idx, total_count):
    print(f"\n{'='*15} 开始注册第 {current_idx}/{total_count} 个账号 {'='*15}")
    
    options = uc.ChromeOptions()
    options.add_argument("--disable-blink-features=AutomationControlled") 
    options.add_argument("--no-first-run")
    options.add_argument("--start-maximized")
    
    driver = uc.Chrome(version_main=144, options=options, use_subprocess=True)
    wait = WebDriverWait(driver, 20)
    
    success = False
    
    try:
        log(f"正在访问: {TARGET_URL}")
        driver.get(TARGET_URL)
        
        # 1. 进入注册页
        wait.until(EC.element_to_be_clickable((By.XPATH, "//button[contains(text(), 'Sign in')] | //a[contains(text(), 'Sign in')]"))).click()
        wait.until(EC.visibility_of_element_located((By.XPATH, "//*[contains(text(), 'Welcome back')]")))
        sign_up_link = wait.until(EC.element_to_be_clickable((By.XPATH, "//a[contains(text(), 'Sign up')] | //button[contains(text(), 'Sign up')]")))
        driver.execute_script("arguments[0].click();", sign_up_link)

        # 2. 填写表单
        wait.until(EC.visibility_of_element_located((By.CSS_SELECTOR, "input[name='emailAddress']")))
        email = create_temp_email()
        if not email: 
            driver.quit()
            return False
            
        password = generate_password()
        driver.find_element(By.CSS_SELECTOR, "input[name='emailAddress']").send_keys(email)
        driver.find_element(By.CSS_SELECTOR, "input[name='password']").send_keys(password)
        
        log("点击 Continue...")
        try:
            driver.find_element(By.XPATH, "//button[contains(text(), 'Continue')]").click()
        except:
            driver.find_element(By.CSS_SELECTOR, "input[name='password']").submit()

        # 3. 强制暴力过校验 (持续 30 秒直到看到验证码输入框)
        log("🛡️ 进入暴力过校验模式...")
        pass_check = False
        for attempt in range(15): # 2s 一次，持续约 30s
            if len(driver.find_elements(By.CSS_SELECTOR, "input[inputmode='numeric']")) > 0:
                log("🚀 验证码输入框已出现！")
                pass_check = True
                break
            
            # 每轮都尝试点击
            force_inject_turnstile(driver)
            time.sleep(2)
            
        if not pass_check:
            log("❌ 暴力过校验超时，跳过此账号", "ERR")
            driver.quit()
            return False

        # 4. 验证码提取
        log("等待输入验证码...")
        otp_input = WebDriverWait(driver, 10).until(EC.presence_of_element_located((By.CSS_SELECTOR, "input[inputmode='numeric']")))
        code = wait_for_code(email)
        
        if code:
            log(f"✍️ 填入: {code}")
            otp_input.send_keys(code)
            time.sleep(5)
            
            # 用户名处理
            if "sign-up" in driver.current_url:
                try:
                    WebDriverWait(driver, 5).until(EC.presence_of_element_located((By.NAME, "username")))
                    driver.find_element(By.NAME, "username").send_keys("User" + str(random.randint(1000, 9999)))
                    btns = driver.find_elements(By.TAG_NAME, "button")
                    for btn in btns:
                        if "Continue" in btn.text:
                            btn.click()
                            break
                except: pass 

            # 使用 CDP 获取所有域名的 Cookie (解决跨子域名获取不到 .clerk.orchids.app 的问题)
            log("🌐 使用 CDP 提取全局 Cookie...")
            try:
                res = driver.execute_cdp_cmd('Network.getAllCookies', {})
                all_cookies = res.get('cookies', [])
            except Exception as e:
                log(f"CDP 获取失败: {e}", "ERR")
                all_cookies = driver.get_cookies()

            log(f"--- 搜索到 {len(all_cookies)} 个全局 Cookie ---")
            client_key = None
            for cookie in all_cookies:
                name = cookie.get('name')
                domain = cookie.get('domain')
                value = cookie.get('value')
                
                # 打印含有 client 关键字的 cookie 供参考
                if 'client' in name.lower():
                    log(f"发现相关 Cookie -> 名称: {name}, 域名: {domain}, 值预览: {value[:30]}...")

                if name == '__client':
                    client_key = value
                    # 如果域名包含 clerk，说明找到了最准确的那个
                    if 'clerk' in domain:
                        log(f"🎯 精准命中 Clerk 域名的 __client")
            
            if client_key:
                log(f"✅ 成功提取 __client Key")
                with open(OUTPUT_FILE, "a") as f:
                    f.write(f"{client_key}\n")
                
                # 同步上传到服务器
                upload_to_server(client_key)
                
                success = True
            else:
                log("❌ 未能提取到 __client Key", "ERR")
                success = False
        else:
            log("❌ 验证码获取超时")

    except Exception as e:
        log(f"❌ 注册流程出错: {e}")
    finally:
        try: driver.quit() 
        except: pass
        
    return success

# ================= 主控制程序 =================

if __name__ == "__main__":
    total_num = 20  # 默认注册 10 个
    success_count = 0
    
    # 清空旧文件，重新按新格式生成
    with open(OUTPUT_FILE, "w") as f: pass 

    for i in range(total_num):
        if register_one_account(i + 1, total_num):
            success_count += 1
        
        if i < total_num - 1:
            wait_time = random.randint(3, 6)
            log(f"☕ 休息 {wait_time} 秒后继续...")
            time.sleep(wait_time)

    print(f"\n{'='*30}")
    print(f"任务结束！成功: {success_count}/{total_num}")
    print(f"数据已保存在: {OUTPUT_FILE}")
