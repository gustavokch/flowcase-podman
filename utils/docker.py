import re
import os
import docker
from utils.logger import log
from __init__ import __version__

docker_client = None

def get_registry_auth():
	"""Return Docker registry auth dict from env, or None if not configured."""
	username = os.getenv("FLOWCASE_DOCKER_USERNAME")
	password = os.getenv("FLOWCASE_DOCKER_PASSWORD")
	if not username or not password:
		return None
	return {"username": username, "password": password}

def image_registry():
	"""Namespace/registry prefix for Flowcase's own images.

	Defaults to the Docker Hub namespace; set FLOWCASE_IMAGE_REGISTRY (e.g.
	"quay.io/gustavokch") to pull them from an alternative registry and avoid
	Docker Hub rate limits.
	"""
	return os.getenv("FLOWCASE_IMAGE_REGISTRY", "flowcaseweb").rstrip("/")

def guac_image():
	"""Fully-qualified Guacamole image reference for the current version."""
	return f"{image_registry()}/flowcase-guac:{__version__}"

def init_docker():
	global docker_client

	if docker_client is not None:
		return docker_client

	try:
		docker_client = docker.DockerClient(base_url=os.getenv("DOCKER_HOST"))
		docker_client.ping()

		# Log in to a private registry only when one is explicitly configured.
		# Public images (e.g. public Quay repos) and base images via a mirror
		# need no auth; per-pull auth_config still covers private pulls. Always
		# attempting a login against the default Docker Hub URL also breaks
		# Podman's /auth endpoint, so it is gated behind an explicit registry.
		auth = get_registry_auth()
		registry = os.getenv("FLOWCASE_DOCKER_REGISTRY")
		if auth and registry:
			try:
				docker_client.login(username=auth["username"], password=auth["password"], registry=registry)
				log("INFO", f"Authenticated to Docker registry {registry} as {auth['username']}")
			except Exception as e:
				log("ERROR", f"Docker registry login failed: {e}")
		elif not auth:
			log("INFO", "No registry credentials configured; using anonymous pulls (fine for public/mirrored images)")

		ensure_default_network()

		return docker_client
		
	except Exception as e:
		print(f"Docker connection failed: {str(e)}")
		docker_client = None
		return None

def is_docker_available():
	"""Return True if the Docker client is initialized and working"""
	return docker_client is not None

def get_docker_version():
	"""Return Docker version or error message if not available"""
	if not docker_client:
		return "Docker not available"
	
	try:
		return docker_client.version()["Version"]
	except Exception as e:
		return f"Error: {str(e)}"

def cleanup_containers(app=None):
	"""Clean up orphaned containers and restart existing ones"""
	if not docker_client:
		print("No Docker client available, skipping container cleanup")
		return
		
	try:
		# Import here to avoid circular imports
		from models.droplet import DropletInstance
		
		print("Starting container cleanup and persistence check")
		
		# Get all instance IDs from the database - handle application context
		instance_ids = []
		if app:
			with app.app_context():
				instances = DropletInstance.query.all()
				instance_ids = [instance.id for instance in instances]
				print(f"Found {len(instance_ids)} active droplet instances in database")
		else:
			# When called without app context, just clean up orphaned containers
			# based on naming pattern without checking database
			print("No application context provided, skipping instance persistence check")
		
		# Get all containers
		containers = docker_client.containers.list(all=True)
		flowcase_containers = 0
		orphaned_containers = 0
		restarted_containers = 0
		
		for container in containers:
			regex = re.compile(r"flowcase_generated_([a-z0-9]+(-[a-z0-9]+)+)", re.IGNORECASE)
			match = regex.match(container.name)
			if match:
				flowcase_containers += 1
				# Extract instance ID from container name
				container_instance_id = container.name.replace("flowcase_generated_", "")
				
				if app:
					# If container doesn't have a corresponding instance in the database, remove it
					if container_instance_id not in instance_ids:
						orphaned_containers += 1
						print(f"Removing orphaned container {container.name} (status: {container.status})")
						try:
							container.stop()
							container.remove()
						except Exception as e:
							print(f"Error removing container {container.name}: {str(e)}")
					else:
						# If container is stopped, restart it
						if container.status != "running":
							restarted_containers += 1
							print(f"Restarting container {container.name} (status: {container.status})")
							try:
								container.restart()
							except Exception as e:
								print(f"Error restarting container {container.name}: {str(e)}")
				else:
					# Without app context, we can't check database, so just keep all containers
					# If container is stopped, restart it
					if container.status != "running":
						restarted_containers += 1
						print(f"Restarting container {container.name} (status: {container.status})")
						try:
							container.restart()
						except Exception as e:
							print(f"Error restarting container {container.name}: {str(e)}")
		
		print(f"Container cleanup complete: {flowcase_containers} flowcase containers found, {orphaned_containers} orphaned containers removed, {restarted_containers} containers restarted")
							
	except Exception as e:
		print(f"Error in container cleanup: {str(e)}")

def cleanup_profile_volumes(user_id=None, droplet=None):
	"""Remove orphaned persistent-profile Docker volumes (flowcase_profile_*).

	Volume names are built as flowcase_profile_<user_id>_<sanitized-template>, so:
	- user_id: removes every volume belonging to that user (exact prefix match).
	  Safe — a deleted user's profile volumes are all dead.
	- droplet: removes that droplet's profile volumes ONLY when its template pins
	  them to the droplet via {droplet_id}. Without {droplet_id} the volume may be
	  shared across droplets by design, so it is left untouched to avoid data loss.
	"""
	if not docker_client:
		print("No Docker client available, skipping profile volume cleanup")
		return

	prefix = "flowcase_profile_"
	sanitize = lambda s: re.sub(r'[^a-zA-Z0-9._-]', '_', str(s))

	user_prefix = f"{prefix}{sanitize(user_id)}_" if user_id is not None else None

	droplet_fragment = None
	if droplet is not None:
		template = droplet.container_persistent_profile_path or ""
		# Only droplet-pinned volumes can be safely attributed to this droplet.
		if "{droplet_id}" in template:
			droplet_fragment = sanitize(droplet.id)

	if user_prefix is None and droplet_fragment is None:
		return

	try:
		removed = 0
		for volume in docker_client.volumes.list():
			name = volume.name
			if not name.startswith(prefix):
				continue

			matches_user = user_prefix is not None and name.startswith(user_prefix)
			matches_droplet = droplet_fragment is not None and droplet_fragment in name[len(prefix):]
			if not (matches_user or matches_droplet):
				continue

			try:
				volume.remove(force=True)
				removed += 1
				print(f"Removed persistent-profile volume {name}")
			except Exception as e:
				print(f"Error removing volume {name}: {str(e)}")

		print(f"Profile volume cleanup complete: {removed} volume(s) removed")
	except Exception as e:
		print(f"Error in profile volume cleanup: {str(e)}")

def force_pull_required_images():
	"""Force pull all required images for Flowcase (called during startup)"""
	if not docker_client:
		print("No Docker client available, skipping required image pull")
		return
		
	try:
		log("INFO", "Starting required image pull for Flowcase...")
		
		# Define all required images for Flowcase
		required_images = [
			# Guacamole image (always required)
			{
				"name": guac_image(),
				"description": "Guacamole VNC Server"
			}
		]
		
		# Add droplet images from database
		from models.droplet import Droplet
		droplets = Droplet.query.all()
		for droplet in droplets:
			if droplet.container_docker_image is None:
				continue
				
			# Construct full image name
			if droplet.container_docker_registry and "docker.io" not in droplet.container_docker_registry:
				registry = droplet.container_docker_registry.rstrip("/")
				image = f"{registry}/{droplet.container_docker_image}"
			else:
				image = droplet.container_docker_image
				
			required_images.append({
				"name": image,
				"description": f"Droplet: {droplet.display_name}"
			})

		# Pull all required images
		for img_info in required_images:
			image_name = img_info["name"]
			description = img_info["description"]
			
			log("INFO", f"Pulling required Docker image {image_name} ({description})")
			try:
				# Extract tag from image name - handle multiple colons properly
				if ":" in image_name:
					# Split on last colon to handle image names with multiple colons
					parts = image_name.rsplit(":", 1)
					base_image = parts[0]
					tag = parts[1]
				else:
					base_image = image_name
					tag = "latest"
				
				docker_client.images.pull(base_image, tag, auth_config=get_registry_auth())
				log("INFO", f"Successfully pulled required Docker image {image_name} ({description})")
			except Exception as e:
				log("ERROR", f"Error pulling required Docker image {image_name} ({description}): {e}")

		log("INFO", "Required image pull for Flowcase completed")

	except Exception as e:
		log("ERROR", f"Error in force_pull_required_images: {str(e)}")

def pull_images():
	"""Pull all required docker images for Flowcase"""
	if not docker_client:
		print("No Docker client available, skipping image pull")
		return
		
	from models.droplet import Droplet

	try:
		# Define all required images for Flowcase
		required_images = [
			# Guacamole image (always required)
			{
				"name": guac_image(),
				"description": "Guacamole VNC Server"
			}
		]

		# Add droplet images from database
		droplets = Droplet.query.all()
		for droplet in droplets:
			if droplet.container_docker_image is None:
				continue

			# Construct full image name
			if droplet.container_docker_registry and "docker.io" not in droplet.container_docker_registry:
				registry = droplet.container_docker_registry.rstrip("/")
				image = f"{registry}/{droplet.container_docker_image}"
			else:
				image = droplet.container_docker_image

			required_images.append({
				"name": image,
				"description": f"Droplet: {droplet.display_name}"
			})

		# Snapshot of tags already present locally, so we only pull what's
		# missing. Pulling images we already have on every sweep needlessly
		# burns registry pull-rate quota.
		local_tags = set()
		for image in docker_client.images.list():
			local_tags.update(image.tags or [])

		# Pull only the required images that are not already present
		for img_info in required_images:
			image_name = img_info["name"]
			description = img_info["description"]

			if image_name in local_tags:
				continue

			log("INFO", f"Pulling required Docker image {image_name} ({description})")
			try:
				# Extract tag from image name - handle multiple colons properly
				if ":" in image_name:
					# Split on last colon to handle image names with multiple colons
					parts = image_name.rsplit(":", 1)
					base_image = parts[0]
					tag = parts[1]
				else:
					base_image = image_name
					tag = "latest"

				docker_client.images.pull(base_image, tag, auth_config=get_registry_auth())
				log("INFO", f"Successfully pulled required Docker image {image_name} ({description})")
			except Exception as e:
				log("ERROR", f"Error pulling required Docker image {image_name} ({description}): {e}")

	except Exception as e:
		log("ERROR", f"Error in pull_images: {str(e)}")

def check_image_exists(registry, image_name):
	"""Check if a Docker image exists locally"""
	if not docker_client:
		return False
	
	try:
		# Construct full image name
		if registry and "docker.io" not in registry:
			registry = registry.rstrip("/")
			full_image = f"{registry}/{image_name}"
		else:
			full_image = image_name
			
		# Check if image exists locally
		images = docker_client.images.list()
		for image in images:
			if full_image in image.tags:
				return True
		return False
	except Exception as e:
		log("ERROR", f"Error checking if image exists: {str(e)}")
		return False

def pull_single_image(registry, image_name):
	"""Pull a single Docker image and return success status and message"""
	if not docker_client:
		return False, "Docker client not available"
	
	try:
		# Validate image name is not empty
		if not image_name or not image_name.strip():
			return False, "Image name cannot be empty"
		
		# Construct full image name
		if registry and "docker.io" not in registry:
			registry = registry.rstrip("/")
			full_image = f"{registry}/{image_name}"
		else:
			full_image = image_name
			
		# Extract tag from image name - handle multiple colons properly
		if ":" in full_image:
			# Split on last colon to handle image names with multiple colons
			parts = full_image.rsplit(":", 1)
			repository = parts[0]
			tag = parts[1]
		else:
			repository = full_image
			tag = "latest"
		
		log("INFO", f"Manually pulling Docker image {full_image}")
		docker_client.images.pull(repository, tag, auth_config=get_registry_auth())
		log("INFO", f"Successfully pulled Docker image {full_image}")
		return True, f"Successfully pulled {full_image}"
		
	except Exception as e:
		error_msg = f"Error pulling Docker image {image_name}: {str(e)}"
		log("ERROR", error_msg)
		return False, error_msg

def get_images_status():
	"""Get status of all required images (downloaded/missing)"""
	if not docker_client:
		return {}
	
	try:
		from models.droplet import Droplet
		
		# Define all required images
		required_images = [
			{
				"id": "guac",
				"name": "Guacamole",
				"image": guac_image(),
				"description": "Guacamole VNC Server"
			}
		]
		
		# Add droplet images from database
		droplets = Droplet.query.all()
		for droplet in droplets:
			if droplet.container_docker_image is None:
				continue
				
			# Construct full image name
			if droplet.container_docker_registry and "docker.io" not in droplet.container_docker_registry:
				registry = droplet.container_docker_registry.rstrip("/")
				full_image = f"{registry}/{droplet.container_docker_image}"
			else:
				full_image = droplet.container_docker_image
				
			required_images.append({
				"id": droplet.id,
				"name": droplet.display_name,
				"image": full_image,
				"description": f"Droplet: {droplet.display_name}"
			})
		
		status = {}
		local_images = docker_client.images.list()
		local_image_tags = []
		for image in local_images:
			local_image_tags.extend(image.tags)
		
		for img_info in required_images:
			# Check if image exists locally using exact match instead of substring
			exists = any(img_info["image"] == tag for tag in local_image_tags)
			
			status[img_info["id"]] = {
				"droplet_name": img_info["name"],
				"image": img_info["image"],
				"exists": exists,
				"description": img_info["description"]
			}
			
		return status
		
	except Exception as e:
		log("ERROR", f"Error getting images status: {str(e)}")
		return {}

def network_exists(network_name):
	"""Check if a Docker network exists"""
	if not docker_client:
		return False
	
	try:
		docker_client.networks.get(network_name)
		return True
	except docker.errors.NotFound:
		return False
	except Exception as e:
		log("ERROR", f"Error checking network existence: {str(e)}")
		return False

def ensure_default_network():
	"""Ensure the flowcase_default_network exists, creating it if necessary"""
	if not docker_client:
		return False
	
	try:
		if not network_exists("flowcase_default_network"):
			log("INFO", "flowcase_default_network not found, creating it")
			docker_client.networks.create("flowcase_default_network", driver="bridge")
			log("INFO", "Successfully created flowcase_default_network")
		return True
	except Exception as e:
		log("ERROR", f"Failed to create flowcase_default_network: {str(e)}")
		return False

def list_available_networks():
	"""List all available Docker networks"""
	if not docker_client:
		return []
	
	try:
		networks = docker_client.networks.list()
		return [{"id": network.id, "name": network.name} for network in networks]
	except Exception as e:
		log("ERROR", f"Error listing networks: {str(e)}")
		return []

def get_network_for_droplet(droplet):
	"""Get the appropriate network for a droplet, with fallback to default"""
	if not docker_client:
		return "flowcase_default_network"
	
	# If droplet has a specific network defined, use it
	if droplet.container_network and droplet.container_network.strip():
		network_name = droplet.container_network.strip()
		
		# Check if network exists
		if network_exists(network_name):
			return network_name
		else:
			log("WARNING", f"Network {network_name} specified for droplet {droplet.display_name} not found, falling back to default")
	
	# Ensure the default network exists before returning it
	ensure_default_network()
	
	return "flowcase_default_network"
