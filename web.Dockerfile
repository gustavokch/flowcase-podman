FROM python:3.11-slim

WORKDIR /flowcase

COPY config /flowcase/config
COPY models /flowcase/models
COPY nginx /flowcase/nginx
COPY routes /flowcase/routes
COPY static /flowcase/static
COPY templates /flowcase/templates
COPY utils /flowcase/utils
COPY __init__.py run.py gunicorn.conf.py /flowcase/
COPY requirements.txt /flowcase

# The app talks to the container engine over the Docker-compatible API socket
# via the docker-py SDK, so no Docker/Podman CLI needs to be installed here.

# Install Python dependencies
RUN pip install --trusted-host pypi.python.org -r requirements.txt