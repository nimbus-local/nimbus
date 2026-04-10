package layers

type LayerVersionContentInput struct {
	S3Bucket        string `json:"S3Bucket,omitempty"`
	S3Key           string `json:"S3Key,omitempty"`
	S3ObjectVersion string `json:"S3ObjectVersion,omitempty"`
	ZipFile         []byte `json:"ZipFile,omitempty"`
}

type LayerVersionContentOutput struct {
	CodeSha256               string `json:"CodeSha256"`
	CodeSize                 int64  `json:"CodeSize"`
	Location                 string `json:"Location"`
	SigningJobArn            string `json:"SigningJobArn,omitempty"`
	SigningProfileVersionArn string `json:"SigningProfileVersionArn,omitempty"`
}

type LayerVersion struct {
	CompatibleArchitectures []string                  `json:"CompatibleArchitectures,omitempty"`
	CompatibleRuntimes      []string                  `json:"CompatibleRuntimes,omitempty"`
	Content                 LayerVersionContentOutput `json:"Content"`
	CreatedDate             string                    `json:"CreatedDate"`
	Description             string                    `json:"Description,omitempty"`
	LayerArn                string                    `json:"LayerArn"`
	LayerVersionArn         string                    `json:"LayerVersionArn"`
	LicenseInfo             string                    `json:"LicenseInfo,omitempty"`
	Version                 int64                     `json:"Version"`
	// internal only:
	LayerName string `json:"-"`
}
