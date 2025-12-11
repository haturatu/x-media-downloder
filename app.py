import os
import re
import json
import random
import hashlib
from urllib.parse import urlparse
from concurrent.futures import ThreadPoolExecutor, as_completed

import requests
from flask import Flask, jsonify, request, send_from_directory
from flask_cors import CORS
from dotenv import load_dotenv
from celery import Celery

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

# --- In-memory Task Store ---
TASK_STORE = {
    'autotag_task_id': None
}
# --------------------------

# --- Celery Configuration ---
app.config.update(
    broker_url='sqla+sqlite:///celery_broker.db',
    result_backend='db+sqlite:///celery_results.db'
)


def make_celery(app):
    celery = Celery(
        app.import_name,
        backend=app.config['result_backend'],
        broker=app.config['broker_url']
    )
    celery.conf.update(app.config)

    class ContextTask(celery.Task):
        def __call__(self, *args, **kwargs):
            with app.app_context():
                return self.run(*args, **kwargs)

    celery.Task = ContextTask
    return celery

celery = make_celery(app)
# --------------------------

# --- Database Initialization ---
with app.app_context():
    database.init_db()
# -----------------------------

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

def autotag_file(filepath, relative_path, image_hash):
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
                raw_tags = tag_data[0].get('tags', {})
                for tag, confidence in raw_tags.items():
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
        
        if database.is_image_processed(image_hash):
            return {"status": "skipped", "reason": "Duplicate image (already processed)"}
        
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
            database.mark_image_as_processed(image_hash)
            relative_path = os.path.relpath(filepath, UPLOAD_FOLDER).replace("\\\\", "/")
            
            # Perform autotagging
            autotag_file(filepath, relative_path, image_hash)
            
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

# --- Celery Tasks ---
@celery.task(name='tasks.download_tweet_media')
def download_tweet_media_task(tweet_url):
    """Celery task to download all media from a single tweet URL."""
    print(f"Starting download for: {tweet_url}")
    try:
        username = extract_username_from_url(tweet_url)
        image_urls = get_tweet_images_alternative(tweet_url)
        if not image_urls:
            print(f"No images found for {tweet_url}")
            return {"url": tweet_url, "success": False, "message": "No images found"}

        result = download_all_images(image_urls, tweet_url, username)
        print(f"Download result for {tweet_url}: {result['downloaded_count']} downloaded, {result['skipped_count']} skipped.")
        return {"url": tweet_url, **result}
    except Exception as e:
        print(f"Error in download task for {tweet_url}: {e}")
        return {"url": tweet_url, "success": False, "message": str(e)}

# --- API endpoints ---

@app.route('/api/download', methods=['POST'])
def download_images_api():
    urls = request.get_json().get('urls', [])
    if not urls: return jsonify({"success": False, "message": "URL list is required"}), 400
    
    count = 0
    for url in urls:
        if (('x.com' in url or 'twitter.com' in url) and '/status/' in url):
            download_tweet_media_task.delay(url)
            count += 1
            
    return jsonify({"success": True, "message": f"{count} download tasks have been queued."})

@app.route('/api/users')
def list_users():
    search_query = request.args.get('q', '').lower()
    page = int(request.args.get('page', 1))
    per_page = int(request.args.get('per_page', 100))

    offset = (page - 1) * per_page

    all_users = []
    if not os.path.exists(UPLOAD_FOLDER): return jsonify({"users": [], "total_pages": 0, "total_items": 0})
    for item in sorted(os.listdir(UPLOAD_FOLDER)):
        if search_query and search_query not in item.lower(): continue
        user_path = os.path.join(UPLOAD_FOLDER, item)
        if os.path.isdir(user_path):
            try:
                tweet_count = len([d for d in os.listdir(user_path) if os.path.isdir(os.path.join(user_path, d))])
                if tweet_count > 0: all_users.append({"username": item, "tweet_count": tweet_count})
            except OSError: continue
    
    total_items = len(all_users)
    users_for_page = all_users[offset:offset + per_page]
    total_pages = (total_items + per_page - 1) // per_page if total_items > 0 else 0

    return jsonify({
        "items": users_for_page,
        "total_items": total_items,
        "per_page": per_page,
        "current_page": page,
        "total_pages": total_pages
    })

@app.route('/api/users/<username>/tweets')
def list_user_tweets(username):
    page = int(request.args.get('page', 1))
    per_page = int(request.args.get('per_page', 100))

    offset = (page - 1) * per_page

    user_path = os.path.join(UPLOAD_FOLDER, username)
    if not os.path.isdir(user_path): return jsonify({"error": "User not found"}), 404
    
    all_tweets = []
    for tweet_id in sorted(os.listdir(user_path), reverse=True):
        tweet_path = os.path.join(user_path, tweet_id)
        if os.path.isdir(tweet_path):
            images_in_tweet = []
            image_paths = []
            for img_file in sorted(os.listdir(tweet_path)):
                if img_file.lower().endswith(('.jpg', '.jpeg', '.png', '.webp', '.gif')):
                    full_path = os.path.join(tweet_path, img_file)
                    relative_path = os.path.relpath(full_path, UPLOAD_FOLDER).replace("\\\\", "/")
                    image_paths.append(relative_path)
                    images_in_tweet.append({"path": relative_path, "tags": []}) # Placeholder for tags
            
            if images_in_tweet:
                tags_map = database.get_tags_for_files(image_paths)
                for img in images_in_tweet:
                    img['tags'] = tags_map.get(img['path'], [])
                all_tweets.append({"tweet_id": tweet_id, "images": images_in_tweet})

    total_items = len(all_tweets)
    tweets_for_page = all_tweets[offset:offset + per_page]
    total_pages = (total_items + per_page - 1) // per_page if total_items > 0 else 0

    return jsonify({
        "items": tweets_for_page,
        "total_items": total_items,
        "per_page": per_page,
        "current_page": page,
        "total_pages": total_pages
    })

@app.route('/api/images')
def get_images():
    sort_mode = request.args.get('sort', 'latest')
    page = int(request.args.get('page', 1))
    per_page = int(request.args.get('per_page', 100))
    search_tags_str = request.args.get('tags', '')

    offset = (page - 1) * per_page

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
            return jsonify({"items": [], "total_pages": 0, "total_items": 0})
        for root, _, files in os.walk(UPLOAD_FOLDER):
            for file in files:
                if file.lower().endswith(('.jpg', '.jpeg', '.png', '.webp', '.gif')):
                    filepath = os.path.join(root, file)
                    relative_path = os.path.relpath(filepath, UPLOAD_FOLDER).replace("\\\\", "/")
                    try:
                        mtime = os.path.getmtime(filepath)
                        all_images.append({"path": relative_path, "mtime": mtime})
                    except OSError: continue
    
    if sort_mode == 'random':
        # For random, we shuffle the whole list and then take a slice,
        # but if we want consistent pagination with random, we'd need to seed or fetch all and page.
        # For simplicity, if random, we return a random sample of `per_page` items from the full list.
        # This means 'page' parameter effectively gets ignored for 'random' sort.
        if len(all_images) > per_page:
            images_for_page = random.sample(all_images, per_page)
        else:
            images_for_page = all_images
        total_items = len(all_images) # Still report total items in DB
    else: # Default to 'latest'
        all_images.sort(key=lambda x: x.get('mtime', 0), reverse=True)
        total_items = len(all_images)
        images_for_page = all_images[offset:offset + per_page]

    # Get tags for the selected images
    image_paths = [img['path'] for img in images_for_page]
    tags_map = database.get_tags_for_files(image_paths)

    for image in images_for_page:
        image.pop('mtime', None)
        image['tags'] = tags_map.get(image['path'], [])

    total_pages = (total_items + per_page - 1) // per_page if total_items > 0 else 0

    return jsonify({
        "items": images_for_page,
        "total_items": total_items,
        "per_page": per_page,
        "current_page": page,
        "total_pages": total_pages
    })

@app.route('/images/<path:filename>')
def serve_image(filename):
    return send_from_directory(UPLOAD_FOLDER, filename)

@app.route('/api/tags')
def get_all_tags_api():
    page = int(request.args.get('page', 1))
    per_page = int(request.args.get('per_page', 100))

    offset = (page - 1) * per_page

    all_tags = database.get_all_tags()
    
    total_items = len(all_tags)
    tags_for_page = all_tags[offset:offset + per_page]
    total_pages = (total_items + per_page - 1) // per_page if total_items > 0 else 0

    return jsonify({
        "items": tags_for_page,
        "total_items": total_items,
        "per_page": per_page,
        "current_page": page,
        "total_pages": total_pages
    })

@celery.task(bind=True)
def _autotag_all_task(self):
    """The actual task of clearing the DB and re-tagging EVERYTHING."""
    print("Starting background task: FORCE AUTOTAG ALL IMAGES.")
    try:
        with app.app_context():
            self.update_state(state='PROGRESS', meta={'current': 0, 'total': 1, 'status': 'Clearing database...'})
            database.delete_all_tags()
            database.clear_all_processed_images()
            print("Database cleared.")

            all_files_to_process = []
            for root, _, files in os.walk(UPLOAD_FOLDER):
                for file in files:
                    if file.lower().endswith(('.jpg', '.jpeg', '.png', '.webp', '.gif')):
                        filepath = os.path.join(root, file)
                        try:
                            with open(filepath, 'rb') as f:
                                image_hash = hashlib.md5(f.read()).hexdigest()
                            relative_path = os.path.relpath(filepath, UPLOAD_FOLDER).replace("\\\\", "/")
                            all_files_to_process.append((filepath, relative_path, image_hash))
                        except Exception as e:
                            print(f"Error processing file {filepath} for hashing: {e}")
            
            total_files = len(all_files_to_process)
            if total_files == 0:
                return {'current': 0, 'total': 0, 'status': 'No images found to process.'}

            print(f"Found {total_files} images to force re-tag.")
            self.update_state(state='PROGRESS', meta={'current': 0, 'total': total_files, 'status': 'Starting processing...'})

            def autotag_and_mark(args):
                filepath, relative_path, image_hash = args
                try:
                    autotag_file(filepath, relative_path, image_hash)
                    database.mark_image_as_processed(image_hash)
                    return True, relative_path # Success, and the path
                except Exception as e:
                    print(f"Error during autotag_and_mark for {relative_path}: {e}")
                    return False, relative_path # Failure, and the path

            futures = {executor.submit(autotag_and_mark, args): args for args in all_files_to_process}
            processed_count = 0
            for i, future in enumerate(as_completed(futures)):
                success, relative_path = future.result()
                if success:
                    processed_count += 1
                self.update_state(state='PROGRESS', meta={'current': processed_count, 'total': total_files, 'status': f'Processed {processed_count}/{total_files} (last: {relative_path}, success: {success})'})

            return {'current': processed_count, 'total': total_files, 'status': f'Complete! Processed {processed_count} files.'}
            
    except Exception as e:
        print(f"An error occurred during the force autotagging task: {e}")
        self.update_state(state='FAILURE', meta={'exc_type': type(e).__name__, 'exc_message': str(e)})
        raise e

@celery.task(bind=True)
def _autotag_untagged_task(self):
    """The task of tagging only files that have no tags."""
    print("Starting background task: autotag untagged files.")
    try:
        with app.app_context():
            self.update_state(state='PROGRESS', meta={'current': 0, 'total': 1, 'status': 'Finding untagged files...'})
            tagged_files = database.get_all_image_filepaths_from_db()
            untagged_files_to_process = []

            for root, _, files in os.walk(UPLOAD_FOLDER):
                for file in files:
                    if file.lower().endswith(('.jpg', '.jpeg', '.png', '.webp', '.gif')):
                        filepath = os.path.join(root, file)
                        relative_path = os.path.relpath(filepath, UPLOAD_FOLDER).replace("\\\\", "/")
                        if relative_path not in tagged_files:
                            try:
                                with open(filepath, 'rb') as f:
                                    image_hash = hashlib.md5(f.read()).hexdigest()
                                untagged_files_to_process.append((filepath, relative_path, image_hash))
                            except Exception as e:
                                print(f"Error processing file {filepath} for hashing: {e}")
            
            total_files = len(untagged_files_to_process)
            if total_files == 0:
                return {'current': 0, 'total': 0, 'status': 'No new untagged images to process.'}

            print(f"Found {total_files} untagged images to process.")
            self.update_state(state='PROGRESS', meta={'current': 0, 'total': total_files, 'status': 'Starting processing...'})
            
            def autotag_and_mark(args):
                filepath, relative_path, image_hash = args
                try:
                    autotag_file(filepath, relative_path, image_hash)
                    database.mark_image_as_processed(image_hash)
                    return True, relative_path # Success, and the path
                except Exception as e:
                    print(f"Error during autotag_and_mark for {relative_path}: {e}")
                    return False, relative_path # Failure, and the path

            futures = {executor.submit(autotag_and_mark, args): args for args in untagged_files_to_process}
            processed_count = 0
            for i, future in enumerate(as_completed(futures)):
                success, relative_path = future.result()
                if success:
                    processed_count += 1
                self.update_state(state='PROGRESS', meta={'current': processed_count, 'total': total_files, 'status': f'Processed {processed_count}/{total_files} (last: {relative_path}, success: {success})'})

            return {'current': processed_count, 'total': total_files, 'status': f'Complete! Processed {processed_count} files.'}

    except Exception as e:
        print(f"An error occurred during the untagged autotagging task: {e}")
        self.update_state(state='FAILURE', meta={'exc_type': type(e).__name__, 'exc_message': str(e)})
        raise e

@app.route('/api/autotag/reload', methods=['POST'])
def autotag_reload_api():
    if not AUTOTAGGER_ENABLED or not AUTOTAGGER_URL:
        return jsonify({"success": False, "message": "Autotagger is not configured."}), 400
    
    task = _autotag_all_task.delay()
    TASK_STORE['autotag_task_id'] = task.id
    
    return jsonify({"success": True, "message": "Started force re-tagging for ALL images in the background.", "task_id": task.id})

@app.route('/api/autotag/untagged', methods=['POST'])
def autotag_untagged_api():
    if not AUTOTAGGER_ENABLED or not AUTOTAGGER_URL:
        return jsonify({"success": False, "message": "Autotagger is not configured."}), 400
    
    task = _autotag_untagged_task.delay()
    TASK_STORE['autotag_task_id'] = task.id
    
    return jsonify({"success": True, "message": "Autotagging for untagged images started in the background.", "task_id": task.id})

@app.route('/api/autotag/status')
def autotag_status_api():
    task_id = TASK_STORE.get('autotag_task_id')
    if not task_id:
        return jsonify({'state': 'NOT_FOUND', 'status': 'No autotagging task has been run yet.'})

    task = celery.AsyncResult(task_id)
    
    if task.state == 'PENDING':
        response = {'state': task.state, 'status': 'Task is pending...'}
    elif task.state == 'PROGRESS':
        info = task.meta or {}
        response = {
            'state': task.state,
            'current': info.get('current', 0),
            'total': info.get('total', 1),
            'status': info.get('status', '')
        }
    elif task.state == 'SUCCESS':
        info = task.info or {}
        response = {
            'state': task.state,
            'current': info.get('current', 0),
            'total': info.get('total', 0),
            'status': info.get('status', 'Task completed successfully.')
        }
    else: # FAILURE or other states
        info = task.info or {}
        status_message = str(task.info)
        if isinstance(info, dict):
            status_message = info.get('exc_message', str(info))
        response = {
            'state': task.state,
            'status': f"Task failed: {status_message}"
        }
        
    return jsonify(response)



@app.route('/api/images/retag', methods=['POST'])
def retag_image_api():
    filepath = request.get_json().get('filepath')
    if not filepath:
        return jsonify({"success": False, "message": "filepath is required"}), 400

    # Check for existing tags first
    existing_tags = database.get_tags_for_files([filepath]).get(filepath, [])
    if existing_tags:
        return jsonify({
            "success": True,
            "message": "Image already has tags.",
            "tags": existing_tags
        })

    full_path = os.path.join(UPLOAD_FOLDER, filepath)
    if not os.path.exists(full_path):
        return jsonify({"success": False, "message": "File not found"}), 404

    try:
        with open(full_path, 'rb') as f:
            image_content = f.read()
        image_hash = hashlib.md5(image_content).hexdigest()
    except IOError as e:
        return jsonify({"success": False, "message": f"Could not read file: {e}"}), 500

    # Run autotagging since no tags exist
    autotag_file(full_path, filepath, image_hash)

    # Fetch the new tags
    new_tags_map = database.get_tags_for_files([filepath])
    new_tags = new_tags_map.get(filepath, [])

    return jsonify({"success": True, "tags": new_tags})