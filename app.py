import os
import re
import json
import random
import hashlib
from urllib.parse import urlparse
from concurrent.futures import ThreadPoolExecutor

import requests
from flask import Flask, jsonify, render_template, request, send_from_directory
from flask_cors import CORS

app = Flask(__name__)
CORS(app)

# --- Configuration ---
UPLOAD_FOLDER = 'downloaded_images'
os.makedirs(UPLOAD_FOLDER, exist_ok=True)
executor = ThreadPoolExecutor(max_workers=5)
# ---------------------

# Set to store existing image hashes
existing_image_hashes = set()

# Load existing image hashes on startup
def load_existing_image_hashes():
    global existing_image_hashes
    existing_image_hashes.clear()
    
    if not os.path.exists(UPLOAD_FOLDER):
        return
    
    for root, _, files in os.walk(UPLOAD_FOLDER):
        for file in files:
            if file.lower().endswith(('.jpg', '.jpeg', '.png', '.webp', '.gif')):
                filepath = os.path.join(root, file)
                try:
                    with open(filepath, 'rb') as f:
                        file_hash = hashlib.md5(f.read()).hexdigest()
                    existing_image_hashes.add(file_hash)
                except Exception as e:
                    print(f"Error reading file {filepath}: {e}")

# Load existing hashes on app startup
load_existing_image_hashes()

def extract_username_from_url(tweet_url):
    match = re.search(r'(?:x|twitter)\.com/([^/]+)/status/', tweet_url)
    return match.group(1) if match else "unknown_user"

def get_tweet_images_alternative(tweet_url):
    try:
        tweet_id = tweet_url.split('/')[-1].split('?')[0]
        api_url = f"https://cdn.syndication.twimg.com/tweet-result?id={tweet_id}&token=4"
        headers = {'User-Agent': 'Mozilla/5.0'}
        response = requests.get(api_url, headers=headers, timeout=10)
        response.raise_for_status()
        data = response.json()
        image_urls = [re.sub(r':\w+$', ':orig', photo.get('url')) for photo in data.get('photos', []) if photo.get('url')]
        return list(set(image_urls))
    except Exception as e:
        print(f"Failed to get images for {tweet_url}: {e}")
        return []

def download_image_task(image_url, tweet_dir, index, tweet_id):
    try:
        headers = {'User-Agent': 'Mozilla/5.0'}
        response = requests.get(image_url, headers=headers, timeout=30)
        response.raise_for_status()
        
        # Calculate image hash
        image_content = response.content
        image_hash = hashlib.md5(image_content).hexdigest()
        
        # Check for duplicates
        if image_hash in existing_image_hashes:
            return {"status": "skipped", "reason": "Duplicate image"}
        
        content_type = response.headers.get('content-type', '')
        ext = '.jpg'
        if 'png' in content_type: ext = '.png'
        elif 'webp' in content_type: ext = '.webp'
        elif 'gif' in content_type: ext = '.gif'
        
        filename = f"{tweet_id}_{index:02d}{ext}"
        filepath = os.path.join(tweet_dir, filename)
        
        # Save image
        with open(filepath, 'wb') as f:
            f.write(image_content)
        
        if os.path.getsize(filepath) > 0:
            # Register hash
            existing_image_hashes.add(image_hash)
            return {"status": "success", "path": os.path.relpath(filepath, UPLOAD_FOLDER).replace("\\", "/")}
        
        os.remove(filepath)
        return {"status": "failed", "reason": "Empty file"}
    except Exception as e:
        return {"status": "failed", "reason": str(e)}

def download_all_images(image_urls, tweet_url, username):
    if not image_urls: return {"success": False, "message": "No images found"}
    user_dir = os.path.join(UPLOAD_FOLDER, username)
    tweet_id = tweet_url.split('/')[-1].split('?')[0]
    tweet_dir = os.path.join(user_dir, tweet_id)
    os.makedirs(tweet_dir, exist_ok=True)
    
    tasks = [executor.submit(download_image_task, url, tweet_dir, i, tweet_id) for i, url in enumerate(image_urls, 1)]
    results = [task.result() for task in tasks]
    
    successes = [r for r in results if r['status'] == 'success']
    skipped = [r for r in results if r['status'] == 'skipped']
    failures = [r for r in results if r['status'] == 'failed']
    
    return {
        "success": len(successes) > 0,
        "downloaded_count": len(successes),
        "skipped_count": len(skipped),
        "files": successes,
        "skipped": skipped,
        "errors": failures
    }

# --- API endpoints ---
@app.route('/')
def index():
    return render_template('index.html')

@app.route('/api/download', methods=['POST'])
def download_images_api():
    urls = request.get_json().get('urls', [])
    if not urls: return jsonify({"success": False, "message": "URL list is required"}), 400
    results = []
    for url in urls:
        if not (('x.com' in url or 'twitter.com' in url) and '/status/' in url):
            results.append({"url": url, "success": False, "message": "Invalid URL"})
            continue
        try:
            username = extract_username_from_url(url)
            image_urls = get_tweet_images_alternative(url)
            if not image_urls:
                results.append({"url": url, "success": False, "message": "No images found"})
                continue
            result = download_all_images(image_urls, url, username)
            results.append({"url": url, **result})
        except Exception as e:
            results.append({"url": url, "success": False, "message": str(e)})
    return jsonify({"results": results})

@app.route('/api/users')
def list_users():
    search_query = request.args.get('q', '').lower()
    users = []
    if not os.path.exists(UPLOAD_FOLDER): return jsonify({"users": []})
    for item in sorted(os.listdir(UPLOAD_FOLDER)):
        if search_query and search_query not in item.lower(): continue
        user_path = os.path.join(UPLOAD_FOLDER, item)
        if os.path.isdir(user_path):
            try:
                tweet_count = len([d for d in os.listdir(user_path) if os.path.isdir(os.path.join(user_path, d))])
                if tweet_count > 0: users.append({"username": item, "tweet_count": tweet_count})
            except OSError: continue
    return jsonify({"users": users})

@app.route('/api/users/<username>/tweets')
def list_user_tweets(username):
    user_path = os.path.join(UPLOAD_FOLDER, username)
    if not os.path.isdir(user_path): return jsonify({"error": "User not found"}), 404
    tweets = []
    for tweet_id in sorted(os.listdir(user_path), reverse=True):
        tweet_path = os.path.join(user_path, tweet_id)
        if os.path.isdir(tweet_path):
            images = []
            for img_file in sorted(os.listdir(tweet_path)):
                if img_file.lower().endswith(('.jpg', '.jpeg', '.png', '.webp', '.gif')):
                    images.append({"path": f"{username}/{tweet_id}/{img_file}"})
            if images: tweets.append({"tweet_id": tweet_id, "images": images})
    return jsonify({"tweets": tweets})

@app.route('/api/images/random')
def random_images():
    """Return 50 random images from all images on the server"""
    all_images = []
    if not os.path.exists(UPLOAD_FOLDER):
        return jsonify({"images": []})

    for root, _, files in os.walk(UPLOAD_FOLDER):
        for file in files:
            if file.lower().endswith(('.jpg', '.jpeg', '.png', '.webp', '.gif')):
                relative_path = os.path.relpath(os.path.join(root, file), UPLOAD_FOLDER).replace("\\", "/")
                all_images.append({"path": relative_path})

    sample_size = min(len(all_images), 50)
    random_sample = random.sample(all_images, sample_size)
    
    return jsonify({"images": random_sample})

@app.route('/images/<path:filename>')
def serve_image(filename):
    return send_from_directory(UPLOAD_FOLDER, filename)

if __name__ == '__main__':
    app.run(debug=True, host='0.0.0.0', port=8888)
