import {
  Injectable,
  ServiceUnavailableException,
  BadRequestException,
} from "@nestjs/common";
import {
  S3Client,
  PutObjectCommand,
  GetObjectCommand,
  HeadObjectCommand,
  CreateBucketCommand,
  HeadBucketCommand,
} from "@aws-sdk/client-s3";
import { getSignedUrl } from "@aws-sdk/s3-request-presigner";
import { DeleteObjectCommand } from "@aws-sdk/client-s3";
import { hash } from "./core";
@Injectable()
export class Storage {
  private client = new S3Client({
    endpoint: process.env.S3_ENDPOINT,
    region: process.env.S3_REGION || "us-east-1",
    forcePathStyle: true,
    credentials: {
      accessKeyId: process.env.S3_ACCESS_KEY || "",
      secretAccessKey: process.env.S3_SECRET_KEY || "",
    },
  });
  private bucket = process.env.S3_BUCKET || "synapse-private";
  async ensureBucket() {
    try {
      await this.client.send(new HeadBucketCommand({ Bucket: this.bucket }));
    } catch {
      if (process.env.NODE_ENV === "production")
        throw new ServiceUnavailableException(
          "Private object storage is not configured.",
        );
      await this.client.send(new CreateBucketCommand({ Bucket: this.bucket }));
    }
  }
  async putText(key: string, text: string) {
    await this.ensureBucket();
    const bytes = Buffer.from(text);
    await this.client.send(
      new PutObjectCommand({
        Bucket: this.bucket,
        Key: key,
        Body: bytes,
        ContentType: "text/plain",
      }),
    );
    return { sha256: hash(bytes), size: bytes.length };
  }
  async uploadUrl(key: string, contentType: string) {
    await this.ensureBucket();
    return getSignedUrl(
      this.client,
      new PutObjectCommand({
        Bucket: this.bucket,
        Key: key,
        ContentType: contentType,
      }),
      { expiresIn: 300 },
    );
  }
  async read(key: string, maxSize = 100 * 1024 * 1024) {
    const head = await this.client.send(
      new HeadObjectCommand({ Bucket: this.bucket, Key: key }),
    );
    if (!head.ContentLength || head.ContentLength > maxSize)
      throw new BadRequestException("Source size is invalid.");
    const result = await this.client.send(
      new GetObjectCommand({ Bucket: this.bucket, Key: key }),
    );
    const bytes = Buffer.from(await result.Body!.transformToByteArray());
    return bytes;
  }
  async remove(key: string) {
    await this.client.send(
      new DeleteObjectCommand({ Bucket: this.bucket, Key: key }),
    );
  }
}
