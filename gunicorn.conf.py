import os
import multiprocessing
import threading
import time
from dotenv import load_dotenv

# Load .env before any os.getenv (e.g. registry credentials in init_docker)
load_dotenv()

from utils.docker import pull_images

bind = "0.0.0.0:5000"

workers = multiprocessing.cpu_count() * 2 + 1
worker_class = "sync"
worker_connections = 1000

max_requests = 1000
max_requests_jitter = 50

timeout = 30
keepalive = 2

accesslog = "-"
errorlog = "-"
loglevel = "info"

proc_name = "flowcase"

preload_app = True

def post_fork(server, worker):
	os.environ['GUNICORN_WORKER_ID'] = str(worker.age)

	from utils.docker import init_docker
	docker_client = init_docker()
	if not docker_client:
		print(f"Warning: Failed to initialize Docker client in worker {worker.age}")

def on_starting(server):
	from __init__ import db, initialize_database_and_setup
	from config.config import configure_app
	from flask import Flask
	from utils.docker import cleanup_containers, init_docker

	init_docker()

	temp_app = Flask(__name__)
	configure_app(temp_app)
	db.init_app(temp_app)
	
	with temp_app.app_context():
		initialize_database_and_setup()
	
	cleanup_containers(temp_app)
	
	# start background thread for periodic image checks.
	# pull_images() only fetches images missing locally, so this just backfills
	# anything added since startup. A long interval avoids hammering the registry
	# pull-rate limit (the previous 60s sweep was the main quota burner).
	PULL_INTERVAL_SECONDS = 6 * 60 * 60  # 6 hours
	def pull_images_worker():
		# Pull missing images once at startup, then re-check every 6h. Both
		# pulls only fetch images not already present locally.
		first = True
		while True:
			try:
				if not first:
					time.sleep(PULL_INTERVAL_SECONDS)
				first = False
				with temp_app.app_context():
					pull_images()
			except Exception as e:
				print(f"Error in pull_images_worker: {e}")
				time.sleep(PULL_INTERVAL_SECONDS)
	
	thread = threading.Thread(target=pull_images_worker, daemon=True)
	thread.start()