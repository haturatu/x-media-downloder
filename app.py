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
from dotenv import load_dotenv

import database

# --- Configuration ---
load_dotenv()
app = Flask(__name__)
CORS(app)

UPLOAD_FOLDER = 'downloaded_images'
os.makedirs(UPLOAD_FOLDER, exist_ok=True)
executor = ThreadPoolExecutor(max_workers=5)

AUTOTAGGER_ENABLED = os.getenv('AUTOTAGGER') == 'true'
AUTOTAGGER_URL = os.getenv('AUTOTAGGER_URL')
# ---------------------

# --- Database Initialization ---
with app.app_context():
    database.init_db()
# -----------------------------

# Set to store existing image hashes
existing_image_hashes = set()

def load_existing_image_hashes():
    global existing_image_hashes
    existing_image_hashes.clear()
    if not os.path.exists(UPLOAD_FOLDER): return
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

def autotag_file(filepath, relative_path):
    if not AUTOTAGGER_ENABLED or not AUTOTAGGER_URL:
        return

    try:
        with open(filepath, 'rb') as f:
            files = {'file': (os.path.basename(filepath), f)}
            response = requests.post(AUTOTAGGER_URL, files=files, data={'format': 'json'}, timeout=60)
            response.raise_for_status()
            tag_data = response.json()

            if isinstance(tag_data, list) and tag_data:
                tags_to_add = []
                # The response is a list with one dictionary
                raw_tags = tag_data[0].get('tags', {})
                for tag, confidence in raw_tags.items():
                    # Add tags with confidence > 0.4
                    if confidence > 0.4:
                        tags_to_add.append({'tag': tag, 'confidence': confidence})
                
                if tags_to_add:
                    database.add_tags_for_file(relative_path, tags_to_add)
                    print(f"Tagged {relative_path} with {len(tags_to_add)} tags.")

    except requests.RequestException as e:
        print(f"Autotagging failed for {relative_path}: {e}")
    except Exception as e:
        print(f"An unexpected error occurred during autotagging for {relative_path}: {e}")


def download_image_task(image_url, tweet_dir, index, tweet_id):
    try:
        headers = {'User-Agent': 'Mozilla/5.0'}
        response = requests.get(image_url, headers=headers, timeout=30)
        response.raise_for_status()
        
        image_content = response.content
        image_hash = hashlib.md5(image_content).hexdigest()
        
        if image_hash in existing_image_hashes:
            return {"status": "skipped", "reason": "Duplicate image"}
        
        content_type = response.headers.get('content-type', '')
        ext = '.jpg'
        if 'png' in content_type: ext = '.png'
        elif 'webp' in content_type: ext = '.webp'
        elif 'gif' in content_type: ext = '.gif'
        
        filename = f"{tweet_id}_{index:02d}{ext}"
        filepath = os.path.join(tweet_dir, filename)
        
        with open(filepath, 'wb') as f:
            f.write(image_content)
        
        if os.path.getsize(filepath) > 0:
            existing_image_hashes.add(image_hash)
            relative_path = os.path.relpath(filepath, UPLOAD_FOLDER).replace("\\", "/")
            
            # Perform autotagging
            autotag_file(filepath, relative_path)
            
            return {"status": "success", "path": relative_path}
        
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
            images_in_tweet = []
            image_paths = []
            for img_file in sorted(os.listdir(tweet_path)):
                if img_file.lower().endswith(('.jpg', '.jpeg', '.png', '.webp', '.gif')):
                    relative_path = f"{username}/{tweet_id}/{img_file}"
                    image_paths.append(relative_path)
                    images_in_tweet.append({"path": relative_path, "tags": []}) # Placeholder for tags
            
            if images_in_tweet:
                tags_map = database.get_tags_for_files(image_paths)
                for img in images_in_tweet:
                    img['tags'] = tags_map.get(img['path'], [])
                tweets.append({"tweet_id": tweet_id, "images": images_in_tweet})

    return jsonify({"tweets": tweets})

@app.route('/api/images')
def get_images():
    sort_mode = request.args.get('sort', 'latest')
    limit = int(request.args.get('limit', 100))
    search_tags_str = request.args.get('tags', '')

    all_images = []
    
    if search_tags_str:
        search_tags = [tag.strip() for tag in search_tags_str.split(',') if tag.strip()]
        if search_tags:
            image_paths = database.find_files_by_tags(search_tags)
            for path in image_paths:
                try:
                    mtime = os.path.getmtime(os.path.join(UPLOAD_FOLDER, path))
                    all_images.append({"path": path, "mtime": mtime})
                except OSError: continue
    else:
        if not os.path.exists(UPLOAD_FOLDER):
            return jsonify({"images": []})
        for root, _, files in os.walk(UPLOAD_FOLDER):
            for file in files:
                if file.lower().endswith(('.jpg', '.jpeg', '.png', '.webp', '.gif')):
                    filepath = os.path.join(root, file)
                    relative_path = os.path.relpath(filepath, UPLOAD_FOLDER).replace("\\", "/")
                    try:
                        mtime = os.path.getmtime(filepath)
                        all_images.append({"path": relative_path, "mtime": mtime})
                    except OSError: continue
    
    if sort_mode == 'random':
        sample_size = min(len(all_images), limit)
        images_to_return = random.sample(all_images, sample_size)
    else: # Default to 'latest'
        all_images.sort(key=lambda x: x.get('mtime', 0), reverse=True)
        images_to_return = all_images[:limit]

    # Get tags for the selected images
    image_paths = [img['path'] for img in images_to_return]
    tags_map = database.get_tags_for_files(image_paths)

    for image in images_to_return:
        image.pop('mtime', None)
        image['tags'] = tags_map.get(image['path'], [])

    return jsonify({"images": images_to_return})

@app.route('/images/<path:filename>')
def serve_image(filename):
    return send_from_directory(UPLOAD_FOLDER, filename)

@app.route('/api/tags')
def get_all_tags_api():
    tags = database.get_all_tags()
    return jsonify({"tags": tags})

def _autotag_all_task():
    """The actual task of iterating and tagging all files."""
    print("Starting background task: autotag all files.")
    with app.app_context():
        database.clear_all_tags()
        image_files = []
        for root, _, files in os.walk(UPLOAD_FOLDER):
            for file in files:
                if file.lower().endswith(('.jpg', '.jpeg', '.png', '.webp', '.gif')):
                    filepath = os.path.join(root, file)
                    relative_path = os.path.relpath(filepath, UPLOAD_FOLDER).replace("\\", "/")
                    image_files.append((filepath, relative_path))
        
        for filepath, relative_path in image_files:
            autotag_file(filepath, relative_path)
            
    print("Finished background task: autotag all files.")

@app.route('/api/autotag/reload', methods=['POST'])
def autotag_reload_api():
    if not AUTOTAGGER_ENABLED or not AUTOTAGGER_URL:
        return jsonify({"success": False, "message": "Autotagger is not configured."}), 400
    
    # Run the tagging process in a background thread
    executor.submit(_autotag_all_task)
    
    return jsonify({"success": True, "message": "Autotagging process started in the background. All existing tags have been cleared."})


if __name__ == '__main__':
    app.run(debug=True, host='0.0.0.0', port=8888)