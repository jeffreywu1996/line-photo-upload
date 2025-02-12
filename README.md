# LINE Photo Bot

A Go application that automatically saves photos from LINE messages to Google Drive.

## Features
- Receives images from LINE messaging API
- Automatically uploads images to specified Google Drive folder
- Supports concurrent message processing
- Prevents duplicate message processing
- Docker support with ngrok integration

## Prerequisites
1. LINE Messaging API account
   - Create a channel at [LINE Developers Console](https://developers.line.biz/console/)
   - Get your Channel Secret and Channel Token

2. Google Cloud Project
   - Create a project at [Google Cloud Console](https://console.cloud.google.com/)
   - Enable Google Drive API
   - Create a Service Account and download credentials
   - Create a Google Drive folder and note its ID

## Setup Instructions

### 1. Google Cloud Setup
1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Create a new project or select existing one
3. Enable Google Drive API:
   - Go to "APIs & Services" > "Library"
   - Search for "Google Drive API"
   - Click "Enable"
4. Create Service Account:
   - Go to "APIs & Services" > "Credentials"
   - Click "Create Credentials" > "Service Account"
   - Fill in service account details
   - Download JSON credentials file
5. Create Google Drive folder:
   - Create a folder in Google Drive
   - Share it with the service account email
   - Copy the folder ID from the URL

### 2. LINE Bot Setup
1. Go to [LINE Developers Console](https://developers.line.biz/console/)
2. Create a new provider (if needed)
3. Create a new channel (Messaging API)
4. Get Channel Secret and Channel Token
5. Set Webhook URL (after deploying your application)

### 3. Local Development Setup

1. Clone the repository:
```bash
git clone https://github.com/yourusername/line-photo-bot
cd line-photo-bot
```

2. Copy example files and configure:
```bash
cp .env.example .env
# Edit .env with your credentials
```

3. Run with Docker:
```bash
docker-compose up --build
```

Or run locally:
```bash
go run main.go
```

4. Start ngrok (if running locally):
```bash
ngrok http 3000
```

5. Update webhook URL in LINE Developers Console with your ngrok URL + "/callback"

## Environment Variables

| Variable | Description |
|----------|-------------|
| LINE_CHANNEL_SECRET | Secret from LINE Messaging API |
| LINE_CHANNEL_TOKEN | Token from LINE Messaging API |
| GOOGLE_APPLICATION_CREDENTIALS | Path to Google service account JSON file |
| GOOGLE_DRIVE_FOLDER_ID | ID of the Google Drive folder for uploads |
| PORT | Server port (default: 3000) |

## Security Notes
- Never commit .env or Google credentials to version control
- Regularly rotate LINE channel tokens and Google credentials
- Use environment variables for all sensitive data

## Contributing
Pull requests are welcome. For major changes, please open an issue first.

## License
[MIT](LICENSE)
